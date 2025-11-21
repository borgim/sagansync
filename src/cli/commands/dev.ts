import path from "node:path";
import chalk from "chalk";
import tar from "tar-fs";
import zlib from "zlib";
import chokidar from "chokidar";
import { loadConfig } from "../utils/config.js";
import { sshExec, scpUpload, type SSHOptions } from "../utils/ssh.js";
import {
  getCurrentBranch,
  getGitHeadPath,
  sanitizeWorkspaceName,
} from "../utils/git.js";

type DevOptions = {
  workspace?: string;
  force?: boolean;
  command?: string;
  build?: boolean;
};

export async function devCommand(opts: DevOptions): Promise<void> {
  const cfg = loadConfig();
  const projectName = path.basename(process.cwd());

  // --- Determinar Workspace ---
  let workspaceName = opts.workspace;
  let currentBranch = getCurrentBranch();

  if (!workspaceName) {
    if (currentBranch) {
      workspaceName = sanitizeWorkspaceName(currentBranch);
      console.log(
        chalk.blue(`ℹ️  Workspace detected: ${chalk.bold(workspaceName)}`)
      );
    } else {
      workspaceName = "production";
    }
  } else {
    workspaceName = sanitizeWorkspaceName(workspaceName);
  }

  // --- Bloqueio de Produção ---
  if (workspaceName === "production" && !opts.force) {
    console.log(
      chalk.red(
        "\n⛔ Safety Lock: Hot-reloading is disabled on the 'production' workspace."
      )
    );
    console.log(
      chalk.gray("   Running dev mode on production can cause instability.")
    );
    console.log(
      chalk.gray(
        "   Use a feature branch or pass --force if you really mean it.\n"
      )
    );
    process.exit(1);
  }

  const containerName = `sagan-${projectName}-${workspaceName}`;
  const imageName = `sagan-${projectName}:${workspaceName}`;
  const remoteDir = `${
    cfg.remotePath || `/srv/${projectName}`
  }/${workspaceName}`;

  const sshOpts: SSHOptions = {
    host: cfg.host,
    user: cfg.ssh?.user,
    port: cfg.ssh?.port,
    identityFile: cfg.ssh?.identityFile,
    strictHostKeyChecking: "no",
  };

  console.log(
    chalk.cyan(`🚀 Starting Dev Mode for: ${chalk.bold(workspaceName)}`)
  );

  // --- Sincronização Inicial (Full Sync) ---
  console.log(chalk.yellow("📦 Performing initial sync..."));

  const pack = tar.pack(process.cwd(), {
    ignore: (name) => {
      const base = path.basename(name);
      return (
        base === "node_modules" ||
        base === ".git" ||
        base === ".sagansync" ||
        base === "dist" ||
        base === ".DS_Store"
      );
    },
  });
  const gzip = zlib.createGzip();
  const tarStream = pack.pipe(gzip);
  const chunks: Buffer[] = [];
  for await (const chunk of tarStream) chunks.push(Buffer.from(chunk));
  const fileBuffer = Buffer.concat(chunks);

  const forceBuild = opts.build ? "true" : "false";

  const setupCmd = `
    export PATH=$PATH:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin && 
    mkdir -p ${remoteDir} && 
    tar -xzf - -C ${remoteDir} && 
    cd ${remoteDir} && 
    if [ "${forceBuild}" = "true" ] || ! podman image exists ${imageName}; then
      echo "🔨 Building Docker image..."
      podman build -t ${imageName} .
    else
      echo "⏩ Image exists. Skipping build. (Use --build to force rebuild)"
    fi
  `;

  await sshExec(sshOpts, {
    command: setupCmd,
    stdin: fileBuffer,
    timeoutMs: 10 * 60_000,
    captureOutput: false,
  });

  // --- Rodar Container em Modo Dev ---
  console.log(chalk.yellow("🔥 Starting container with volume mount..."));

  const internalPort = cfg.internalPort || 3000;
  const devStartCmd = opts.command || "npm run start:dev";
  console.log(chalk.gray(`   Command: ${devStartCmd}`));

  const runDevCmd = `
    export PATH=$PATH:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin &&
    (podman network exists sagan-net || podman network create sagan-net) &&
    podman stop ${containerName} > /dev/null 2>&1 || true && 
    podman rm ${containerName} > /dev/null 2>&1 || true && 
    podman run -d --name ${containerName} \
      --network sagan-net \
      -p 127.0.0.1::${internalPort} \
      -v ${remoteDir}:/app:Z \
      -v /app/node_modules \
      -e NODE_ENV=development \
      ${imageName} ${devStartCmd}
  `;

  try {
    await sshExec(sshOpts, { command: runDevCmd });
  } catch (e) {
    console.log(
      chalk.red(`Failed to start dev container with '${devStartCmd}'.`)
    );
    console.log(chalk.yellow("Falling back to default image command..."));
    await sshExec(sshOpts, {
      command: runDevCmd.replace(devStartCmd, ""),
    });
  }

  // --- Configura Caddy (Roteamento) ---
  console.log(chalk.gray("🔗 Configuring routing..."));

  let hostPort: string | undefined;
  try {
    const portResult = await sshExec(sshOpts, {
      command: `export PATH=$PATH:/usr/local/bin:/usr/sbin && podman port ${containerName} ${internalPort}`,
      captureOutput: true,
    });
    hostPort = portResult.stdout
      ?.trim()
      .split("\n")[0]
      .split(":")
      .pop()
      ?.trim();
  } catch (e) {
    console.log(
      chalk.yellow("⚠️  Could not detect port mapping. URL routing might fail.")
    );
  }

  if (cfg.domain && hostPort) {
    const isProd = workspaceName === "production";
    const deployDomain = isProd ? cfg.domain : `${workspaceName}.${cfg.domain}`;

    const caddyConfig = `${deployDomain} {\\n  reverse_proxy 127.0.0.1:${hostPort}\\n}`;
    const confFile = `/etc/caddy/conf.d/${projectName}-${workspaceName}.caddy`;

    try {
      await sshExec(sshOpts, {
        command: `printf "${caddyConfig}" | sudo tee ${confFile} > /dev/null && sudo systemctl reload caddy`,
      });
      console.log(chalk.green(`🌍 Dev URL: https://${deployDomain}`));
    } catch (e: any) {
      console.log(
        chalk.yellow(
          "\n⚠️  Failed to configure public URL (Caddy reload failed)."
        )
      );
      console.log(chalk.gray("   Logs on VPS: 'systemctl status caddy'"));
    }
  }

  // --- Inicia Watcher ---
  console.log(
    chalk.green.bold("\n👀 Watching for file changes... (Ctrl+C to stop)")
  );

  const watcher = chokidar.watch(".", {
    ignored: /(node_modules|.git|.sagansync|dist|.DS_Store)/,
    persistent: true,
    ignoreInitial: true,
  });

  watcher.on("all", async (event, filePath) => {
    console.log(chalk.gray(`[${event}] ${filePath}`));

    const localPath = path.resolve(filePath);
    const remoteFilePath = path.join(remoteDir, filePath).replace(/\\/g, "/");

    try {
      if (event === "add" || event === "change") {
        await scpUpload(localPath, remoteFilePath, sshOpts);
      } else if (event === "unlink") {
        await sshExec(sshOpts, { command: `rm -f ${remoteFilePath}` });
      }
    } catch (err) {
      console.error(chalk.red(`Sync failed for ${filePath}`), err);
    }
  });

  // --- Trava de Segurança ---
  const gitHeadPath = getGitHeadPath();

  if (gitHeadPath) {
    const gitWatcher = chokidar.watch(gitHeadPath, { ignoreInitial: true });

    gitWatcher.on("change", () => {
      console.log(
        chalk.bgRed.white.bold("\n\n 🛑 CRITICAL: GIT BRANCH CHANGED! ")
      );
      console.log(
        chalk.red(
          `SaganSync Dev stopped to prevent corrupting workspace '${workspaceName}'.`
        )
      );
      console.log(
        chalk.yellow(
          `Please switch back to '${currentBranch}' or run 'sagansync dev' again.`
        )
      );
      process.exit(0);
    });
  } else {
    console.log(
      chalk.yellow("⚠️  Git repository not detected. Safety lock is disabled.")
    );
  }
}
import path from "node:path";
import chalk from "chalk";
import tar from "tar-fs";
import zlib from "zlib";
import { confirm } from "@inquirer/prompts";
import { loadConfig } from "../utils/config.js";
import { sshExec, type SSHOptions } from "../utils/ssh.js";
import { getCurrentBranch, sanitizeWorkspaceName } from "../utils/git.js";

type DeployOptions = {
  workspace?: string;
  force?: boolean;
  verbose?: boolean;
};

export async function deployCommand(opts: DeployOptions): Promise<void> {
  const cfg = loadConfig();
  const projectName = path.basename(process.cwd());

  const silence = opts.verbose ? "" : " > /dev/null 2>&1";

  // --- Determinar Workspace ---
  let workspaceName = opts.workspace;

  if (!workspaceName) {
    const branch = getCurrentBranch();
    if (branch) {
      workspaceName = sanitizeWorkspaceName(branch);
      console.log(
        chalk.blue(
          `ℹ️  Workspace detected from branch '${branch}': ${chalk.bold(
            workspaceName
          )}`
        )
      );
    } else {
      console.log(
        chalk.yellow(
          "⚠️  Git not detected. Using default workspace 'production'."
        )
      );
      workspaceName = "production";
    }
  } else {
    workspaceName = sanitizeWorkspaceName(workspaceName);
  }

  const isProd = workspaceName === "production";

  // Definições de Nomes
  const containerName = `sagan-${projectName}-${workspaceName}`;
  const imageName = `sagan-${projectName}:${workspaceName}`;

  // Definição de Domínio
  let deployDomain: string | undefined;
  if (cfg.domain) {
    deployDomain = isProd ? cfg.domain : `${workspaceName}.${cfg.domain}`;
  }

  const sshOpts: SSHOptions = {
    host: cfg.host,
    user: cfg.ssh?.user,
    port: cfg.ssh?.port,
    identityFile: cfg.ssh?.identityFile,
    strictHostKeyChecking: "no",
  };

  // --- Trava de Segurança (Confirmação) ---
  if (!opts.force) {
    try {
      if (opts.verbose) console.log(chalk.gray("🔍 Checking existing deployment..."));
      
      const checkResult = await sshExec(sshOpts, {
        command: `export PATH=$PATH:/usr/local/bin:/usr/sbin && podman ps -a --filter name=^/${containerName}$ --format "{{.Status}}"`,
        captureOutput: true,
      });

      const status = checkResult.stdout?.trim();

      if (status) {
        console.log(
          chalk.yellow(
            `⚠️  A deploy already exists for workspace '${chalk.bold(
              workspaceName
            )}'.`
          )
        );
        console.log(chalk.gray(`   Container: ${containerName}`));
        console.log(chalk.gray(`   Status: ${status}`));
        if (deployDomain)
          console.log(chalk.gray(`   URL: https://${deployDomain}`));

        const shouldOverwrite = await confirm({
          message: "Do you want to overwrite the current deployment?",
          default: true,
        });

        if (!shouldOverwrite) {
          console.log(chalk.red("Deploy cancelled."));
          process.exit(0);
        }
      }
    } catch (e) {
      if (opts.verbose) console.error(chalk.red("Failed check:"), e);
    }
  }

  console.log(
    chalk.cyan(`🚀 Starting deploy for: ${chalk.bold(workspaceName)}`)
  );
  if (deployDomain)
    console.log(chalk.gray(`   Target URL: https://${deployDomain}`));

  // --- Empacotamento ---
  console.log(chalk.yellow("📦 Packing source code..."));

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

  // --- Upload and Build ---
  const remoteDir = `${
    cfg.remotePath || `/srv/${projectName}`
  }/${workspaceName}`;

  console.log(
    chalk.yellow(
      `📤 Uploading (${(fileBuffer.length / 1024 / 1024).toFixed(
        2
      )} MB) and Building...`
    )
  );

  const uploadAndBuildCmd = `
    export PATH=$PATH:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin && 
    mkdir -p ${remoteDir} && 
    tar -xzf - -C ${remoteDir} && 
    cd ${remoteDir} && 
    podman build -t ${imageName} .
  `;

  await sshExec(sshOpts, {
    command: uploadAndBuildCmd,
    stdin: fileBuffer,
    timeoutMs: 20 * 60_000,
    captureOutput: false,
  });

  // --- Rodar Container ---
  console.log(chalk.yellow("🚀 Starting container..."));

  const internalPort = cfg.internalPort || 3000;

  const runCmd = `
    export PATH=$PATH:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin &&
    (podman network exists sagan-net || podman network create sagan-net)${silence} &&
    podman stop ${containerName}${silence} || true && 
    podman rm ${containerName}${silence} || true && 
    podman run -d --restart always --name ${containerName} --network sagan-net -p 127.0.0.1::${internalPort} ${imageName}
  `;

  await sshExec(sshOpts, { command: runCmd });

  // --- Discovery ---
  console.log(chalk.gray("🔍 Discovering allocated port..."));

  const portResult = await sshExec(sshOpts, {
    command: `export PATH=$PATH:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin && podman port ${containerName} ${internalPort}`,
    captureOutput: true,
  });

  const portOutput = portResult.stdout?.trim();

  if (!portOutput) {
    console.error(chalk.red("Debug Info - Containers running:"));
    await sshExec(sshOpts, {
      command: `export PATH=$PATH:/usr/local/bin:/usr/sbin && podman ps`,
      captureOutput: false,
    });
    throw new Error(
      "Failed to discover the allocated port. The container started correctly?"
    );
  }

  const firstMapping = portOutput.split("\n")[0];
  const hostPort = firstMapping.split(":").pop()?.trim();

  if (!hostPort || isNaN(Number(hostPort))) {
    throw new Error(`Unexpected port format: ${portOutput}`);
  }

  if (opts.verbose) console.log(chalk.gray(`   Container mapped to host port: ${hostPort}`));

  // --- Caddy Routing ---
  if (deployDomain) {
    console.log(chalk.yellow(`🔗 Configuring Caddy for ${deployDomain}...`));

    const caddyConfig = `${deployDomain} {
  reverse_proxy 127.0.0.1:${hostPort}
}`;

    const confFile = `/etc/caddy/conf.d/${projectName}-${workspaceName}.caddy`;

    const updateCaddyCmd = `
      echo '${caddyConfig}' | sudo tee ${confFile}${silence} && 
      sudo systemctl reload caddy
    `;

    await sshExec(sshOpts, { command: updateCaddyCmd });

    console.log(chalk.green(`\n✅ Deploy successful!`));
    console.log(chalk.green(`🌍 Access: https://${deployDomain}`));
  } else {
    console.log(
      chalk.green(`\n✅ Deploy successful (No public domain configured).`)
    );
    console.log(
      chalk.gray(
        `   The service is running on the local port ${hostPort} of the VPS.`
      )
    );
  }
}
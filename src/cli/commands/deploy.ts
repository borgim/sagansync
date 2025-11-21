import fs from "node:fs";
import path from "node:path";
import chalk from "chalk";
import tar from "tar-fs";
import zlib from "zlib";
import { loadConfig } from "../utils/config.js";
import { sshExec, type SSHOptions } from "../utils/ssh.js";
import { getCurrentBranch, sanitizeWorkspaceName } from "../utils/git.js";

type DeployOptions = {
  workspace?: string;
};

export async function deployCommand(opts: DeployOptions): Promise<void> {
  const cfg = loadConfig();
  const projectName = path.basename(process.cwd());

  let workspaceName = opts.workspace;

  if (!workspaceName) {
    const branch = getCurrentBranch();
    if (branch) {
      workspaceName = sanitizeWorkspaceName(branch);
      console.log(chalk.blue(`ℹ️  Workspace detected from branch '${branch}': ${chalk.bold(workspaceName)}`));
    } else {
      console.log(chalk.yellow("⚠️  Git not detected. Using default workspace 'production'."));
      workspaceName = "production";
    }
  } else {
    workspaceName = sanitizeWorkspaceName(workspaceName);
  }

  const isProd = workspaceName === "production";
  
  const containerName = `sagan-${projectName}-${workspaceName}`;
  const imageName = `sagan-${projectName}:${workspaceName}`;
  
  let deployDomain: string | undefined;
  if (cfg.domain) {
    deployDomain = isProd ? cfg.domain : `${workspaceName}.${cfg.domain}`;
  }

  console.log(chalk.cyan(`🚀 Starting deploy for: ${chalk.bold(workspaceName)}`));
  if (deployDomain) console.log(chalk.gray(`   Target URL: https://${deployDomain}`));

  const sshOpts: SSHOptions = {
    host: cfg.host,
    user: cfg.ssh?.user,
    port: cfg.ssh?.port,
    identityFile: cfg.ssh?.identityFile,
    strictHostKeyChecking: "no",
  };

  console.log(chalk.yellow("📦 Packing source code..."));

  const pack = tar.pack(process.cwd(), {
    ignore: (name) => {
      const base = path.basename(name);
      return base === "node_modules" || base === ".git" || base === ".sagansync" || base === "dist";
    },
  });

  const gzip = zlib.createGzip();
  const tarStream = pack.pipe(gzip);

  const chunks: Buffer[] = [];
  for await (const chunk of tarStream) chunks.push(Buffer.from(chunk));
  const fileBuffer = Buffer.concat(chunks);

  const remoteDir = `${cfg.remotePath || `/srv/${projectName}`}/${workspaceName}`;

  console.log(chalk.yellow(`📤 Uploading (${(fileBuffer.length / 1024 / 1024).toFixed(2)} MB) and Building...`));

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

  console.log(chalk.yellow("🚀 Starting container..."));

  const internalPort = cfg.internalPort || 3000;

  const runCmd = `
    export PATH=$PATH:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin &&
    podman stop ${containerName} > /dev/null 2>&1 || true && 
    podman rm ${containerName} > /dev/null 2>&1 || true && 
    podman run -d --restart always --name ${containerName} -p 127.0.0.1::${internalPort} ${imageName}
  `;

  await sshExec(sshOpts, { command: runCmd });

  console.log(chalk.gray("🔍 Discovering allocated port..."));

  const portResult = await sshExec(sshOpts, {
    command: `export PATH=$PATH:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin && podman port ${containerName} ${internalPort}`,
    captureOutput: true,
  });

  const portOutput = portResult.stdout?.trim();
  
  if (!portOutput) {
    console.error(chalk.red("Debug Info - Containers running:"));
    await sshExec(sshOpts, { command: `podman ps`, captureOutput: false });
    throw new Error("Failed to discover the allocated port. The container started correctly?");
  }

  const firstMapping = portOutput.split('\n')[0];
  const hostPort = firstMapping.split(":").pop()?.trim();

  if (!hostPort || isNaN(Number(hostPort))) {
      throw new Error(`Unexpected port format: ${portOutput}`);
  }

  console.log(chalk.gray(`   Container mapped to host port: ${hostPort}`));

  if (deployDomain) {
    console.log(chalk.yellow(`🔗 Configuring Caddy for ${deployDomain}...`));

    const caddyConfig = `${deployDomain} {
  reverse_proxy 127.0.0.1:${hostPort}
}`;

    const confFile = `/etc/caddy/conf.d/${projectName}-${workspaceName}.caddy`;

    const updateCaddyCmd = `
      echo '${caddyConfig}' | sudo tee ${confFile} > /dev/null && 
      sudo systemctl reload caddy
    `;

    await sshExec(sshOpts, { command: updateCaddyCmd });

    console.log(chalk.green(`\n✅ Deploy successful!`));
    console.log(chalk.green(`🌍 Access: https://${deployDomain}`));
  } else {
    console.log(chalk.green(`\n✅ Deploy successful (No public domain configured).`));
    console.log(chalk.gray(`   The service is running on the local port ${hostPort} of the VPS.`));
  }
}
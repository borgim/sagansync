import chalk from "chalk";
import Table from "cli-table3";
import { loadConfig } from "../utils/config.js";
import { sshExec, type SSHOptions } from "../utils/ssh.js";

export async function listCommand(): Promise<void> {
  const cfg = loadConfig();
  
  const sshOpts: SSHOptions = {
    host: cfg.host,
    user: cfg.ssh?.user,
    port: cfg.ssh?.port,
    identityFile: cfg.ssh?.identityFile,
    strictHostKeyChecking: "no",
  };

  console.log(chalk.gray("🔍 Searching for active deployments..."));

  try {
    const podmanCmd = `export PATH=$PATH:/usr/local/bin:/usr/sbin && podman ps -a --filter name=sagan- --format "{{.Names}}|{{.Status}}|{{.Ports}}|{{.CreatedAt}}"`;
    
    const podmanResult = await sshExec(sshOpts, {
      command: podmanCmd,
      captureOutput: true,
    });

    const caddyCmd = `grep -r "{" /etc/caddy/conf.d/ | awk -F: '{print $1, $2}'`;
    
    const caddyResult = await sshExec(sshOpts, {
      command: caddyCmd,
      captureOutput: true,
    });

    const domainMap = new Map<string, string>();
    const caddyLines = (caddyResult.stdout || "").split("\n");
    
    caddyLines.forEach(line => {
      const parts = line.split(" ");
      if (parts.length >= 2) {
        const filePath = parts[0];
        const domain = parts.slice(1).join(" ").replace("{", "").trim();
        const baseName = filePath.split("/").pop()?.replace(".caddy", "");
        if (baseName) domainMap.set(baseName, domain);
      }
    });

    const lines = (podmanResult.stdout || "").trim().split("\n");

    if (!lines[0] || lines.length === 0 || (lines.length === 1 && lines[0] === "")) {
      console.log(chalk.yellow("\nNo deployment found."));
      return;
    }

    const table = new Table({
      head: [
        chalk.cyan.bold('Workspace'), 
        chalk.cyan.bold('Status'), 
        chalk.cyan.bold('Port'), 
        chalk.cyan.bold('URL'), 
        chalk.cyan.bold('Created')
      ],
      style: {
        head: [],
        border: []
      }
    });

    lines.forEach((line) => {
      const parts = line.split("|");
      if (parts.length < 4) return;

      const [name, statusRaw, ports, createdRaw] = parts;
      
      const coreName = name.replace(/^sagan-/, "");
      let url = domainMap.get(coreName) || "No public URL";
      if (url !== "No public URL") url = `https://${url}`;

      const portClean = ports.split("->")[0]?.split(":").pop() || "N/A";

      let statusStyled = statusRaw;
      if (statusRaw.toLowerCase().includes("up")) {
        statusStyled = chalk.green(statusRaw.split(" ago")[0]);
      } else if (statusRaw.toLowerCase().includes("exited")) {
        statusStyled = chalk.red(statusRaw);
      }

      const dateClean = createdRaw.split(".")[0] || createdRaw;

      table.push([
        chalk.white.bold(coreName),
        statusStyled,
        portClean,
        chalk.blue(url),
        chalk.gray(dateClean)
      ]);
    });

    console.log("");
    console.log(table.toString());
    console.log("");

  } catch (error: any) {
    console.error(chalk.red("Error listing deployments:"), error.message);
  }
}
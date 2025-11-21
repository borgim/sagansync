import chalk from "chalk";
import { select, confirm } from "@inquirer/prompts";
import { loadConfig } from "../utils/config.js";
import { sshExec, type SSHOptions } from "../utils/ssh.js";

type RemoveOptions = {
  full?: boolean;
  verbose?: boolean;
};

export async function removeCommand(opts: RemoveOptions): Promise<void> {
  const cfg = loadConfig();
  
  const silence = opts.verbose ? "" : " > /dev/null 2>&1";

  const sshOpts: SSHOptions = {
    host: cfg.host,
    user: cfg.ssh?.user,
    port: cfg.ssh?.port,
    identityFile: cfg.ssh?.identityFile,
    strictHostKeyChecking: "no",
  };

  console.log(chalk.gray("🔍 Searching for deployments to remove..."));

  try {
    const result = await sshExec(sshOpts, {
      command: `export PATH=$PATH:/usr/local/bin:/usr/sbin && podman ps -a --filter name=sagan- --format "{{.Names}}"`,
      captureOutput: true,
    });

    const containers = (result.stdout || "").trim().split("\n").filter(Boolean);

    if (containers.length === 0) {
      console.log(chalk.yellow("No active deployments found."));
      return;
    }

    const selectedContainer = await select({
      message: "Which deployment do you want to remove?",
      choices: containers.map((c) => ({
        name: c,
        value: c,
      })),
    });

    const sure = await confirm({
      message: `Are you sure you want to destroy ${chalk.red(selectedContainer)}?`,
      default: false,
    });

    if (!sure) {
      console.log(chalk.yellow("Operation cancelled."));
      return;
    }

    let removeImage = opts.full || false;
    
    if (!removeImage) {
      removeImage = await confirm({
        message: "Do you also want to remove the Docker Image to save space?",
        default: false
      });
    }

    console.log(chalk.red(`🔥 Removing ${selectedContainer}...`));

    let imageName = "";
    if (removeImage) {
      try {
        const imgResult = await sshExec(sshOpts, {
          command: `export PATH=$PATH:/usr/local/bin:/usr/sbin && podman inspect --format '{{.ImageName}}' ${selectedContainer}`,
          captureOutput: true
        });
        imageName = imgResult.stdout?.trim() || "";
      } catch (e) {
        if (opts.verbose) console.error(e);
      }
    }

    const coreName = selectedContainer.replace(/^sagan-/, "");
    const caddyFile = `/etc/caddy/conf.d/${coreName}.caddy`;

    const removeCmd = `
      export PATH=$PATH:/usr/local/bin:/usr/sbin &&
      podman stop ${selectedContainer}${silence} || true &&
      podman rm -v ${selectedContainer}${silence} || true && 
      rm -f ${caddyFile} &&
      systemctl reload caddy
    `;

    await sshExec(sshOpts, { command: removeCmd });
    
    console.log(chalk.green(`✅ ${selectedContainer} container removed.`));

    if (removeImage && imageName) {
      console.log(chalk.gray(`Cleaning up image: ${imageName}...`));
      try {
        await sshExec(sshOpts, {
          command: `export PATH=$PATH:/usr/local/bin:/usr/sbin && podman rmi ${imageName}`,
          captureOutput: !opts.verbose 
        });
        console.log(chalk.green(`✅ Image removed.`));
      } catch (e) {
        if (opts.verbose) {
             console.log(chalk.yellow(`⚠️  Could not remove image:`));
             console.error(e);
        } else {
             console.log(chalk.yellow(`⚠️  Could not remove image (might be in use). Use --verbose to see details.`));
        }
      }
    }

  } catch (e: any) {
    console.error(chalk.red("Error during removal:"), e.message);
  }
}
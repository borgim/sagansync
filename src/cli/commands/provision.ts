import chalk from "chalk";
import { loadConfig } from "../utils/config";
import { sshExec, type SSHOptions } from "../utils/ssh";
import { confirm } from "@inquirer/prompts";

// @ts-ignore
import provisionScript from "../scripts/provision.sh";

type ProvisionOptions = {
  clean?: boolean;
  force?: boolean;
};

export async function provisionCommand(opts: ProvisionOptions): Promise<void> {
  const cfg = loadConfig();

  const sshOpts: SSHOptions = {
    host: cfg.host,
    user: cfg.ssh?.user,
    port: cfg.ssh?.port ?? 22,
    identityFile: cfg.ssh?.identityFile,
    strictHostKeyChecking: "no",
  };

  const script: string = provisionScript;

  console.log(
    chalk.gray(
      `Host: ${sshOpts.user ? sshOpts.user + "@" : ""}${cfg.host.replace(
        /^.*@/,
        ""
      )}`
    )
  );

  if (opts.clean) {
    console.log(chalk.yellow("🧹 Clean mode requested."));

    if (!opts.force) {
      console.log(
        chalk.gray("🔍 Checking if there are any running applications...")
      );

      try {
        const checkResult = await sshExec(sshOpts, {
          command: `export PATH=$PATH:/usr/local/bin:/usr/sbin && podman ps --filter name=sagan- --format "{{.Names}} ({{.Status}})"`,
          captureOutput: true,
        });

        const activeApps = checkResult.stdout?.trim();

        if (activeApps && activeApps.length > 0) {
          console.log(
            chalk.bgRed.white.bold("\n 🛑 DANGER: APPLICATIONS DETECTED! ")
          );
          console.log(
            chalk.red(
              "The --clean mode will PERMANENTLY DELETE the following containers and Caddy:"
            )
          );
          console.log(
            chalk.gray("------------------------------------------------")
          );
          console.log(activeApps);
          console.log(
            chalk.gray("------------------------------------------------")
          );

          const confirmed = await confirm({
            message: "Are you sure you want to destroy everything and reinstall?",
            default: false,
          });

          if (!confirmed) {
            console.log(chalk.yellow("Operation cancelled."));
            process.exit(0);
          }
        } else {
          console.log(chalk.gray("No SaganSync active application detected."));
        }
      } catch (e) {
        console.error(
          chalk.red("Failed to check if there are any running applications.")
        );
        console.error(e);
        process.exit(1);
      }
    } else {
      console.log(chalk.red.bold("Skipping confirmation (--force enabled)."));
    }
  }

  try {
    await sshExec(sshOpts, {
      command: `bash -lc ${shArg(
        `REMOTE_PATH="\${REMOTE_PATH}" CLEAN_INSTALL="\${CLEAN_INSTALL}" bash -s`
      )}`,
      remoteEnv: {
        REMOTE_PATH: cfg.remotePath ?? "/opt/sagansync",
        CLEAN_INSTALL: opts.clean ? "true" : "false",
      },
      stdin: script,
      allocatePty: false,
      captureOutput: false,
      timeoutMs: 10 * 60_000,
    });

    console.log(chalk.green("\n✅ Provision finished successfully."));
  } catch (err: any) {
    console.error(chalk.red("\n❌ Provision failed."));
    console.error(err.message || err);
    process.exit(1);
  }
}

function shArg(v: string) {
  return `'${v.replace(/'/g, `'\"'\"'`)}'`;
}

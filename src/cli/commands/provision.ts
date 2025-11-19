import fs from "fs";
import chalk from "chalk";
import { loadConfig } from "../utils/config";
import { sshExec, type SSHOptions } from "../utils/ssh";

// @ts-ignore
import provisionScript from "../scripts/provision.sh";

type ProvisionOptions = {
  clean?: boolean;
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

  console.log(chalk.gray(`Host: ${sshOpts.user ? sshOpts.user + "@" : ""}${cfg.host.replace(/^.*@/, "")}`));

  if (opts.clean) {
    console.log(chalk.yellow("🧹 Clean mode enabled: Previous installations will be removed."));
  }

  try {
    await sshExec(sshOpts, {
      command: `bash -lc ${shArg(`REMOTE_PATH="\${REMOTE_PATH}" CLEAN_INSTALL="\${CLEAN_INSTALL}" bash -s`)}`,
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

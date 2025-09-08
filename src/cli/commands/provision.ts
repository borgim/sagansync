import fs from "fs";
import chalk from "chalk";
import { loadConfig } from "../utils/config";
import { sshExec, type SSHOptions } from "../utils/ssh";

// @ts-ignore
import provisionScript from "../scripts/provision.sh";

export async function provisionCommand(): Promise<void> {
  const cfg = loadConfig();

  const sshOpts: SSHOptions = {
    host: cfg.host,                 // pode ser "user@host"
    user: cfg.ssh?.user,            // ou separar
    port: cfg.ssh?.port ?? 22,
    identityFile: cfg.ssh?.identityFile,
    strictHostKeyChecking: "accept-new", // bom equilíbrio entre DX e segurança
  };

  const script: string = provisionScript;

  console.log(chalk.gray(`Host: ${sshOpts.user ? sshOpts.user + "@" : ""}${cfg.host.replace(/^.*@/, "")}`));

  await sshExec(sshOpts, {
    // exporta REMOTE_PATH no remoto e executa bash lendo do stdin
    command: `bash -lc ${shArg(`REMOTE_PATH="\${REMOTE_PATH}" bash -s`)}`,
    remoteEnv: {
      REMOTE_PATH: cfg.remotePath ?? "/opt/sagansync",
    },
    stdin: script,
    allocatePty: false,     // mude para true se algum passo exigir TTY
    captureOutput: false,   // mude para true se quiser coletar stdout/stderr
    timeoutMs: 5 * 60_000, // 4 minutes
  });

  console.log(chalk.green("✅ Provision concluído."));
}

/** Pequeno helper local para citar argumento de forma segura igual ao shQuote do utils */
function shArg(v: string) {
  return `'${v.replace(/'/g, `'\"'\"'`)}'`;
}

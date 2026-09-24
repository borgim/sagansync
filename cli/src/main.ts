import { confirm } from "@inquirer/prompts";
import { Command, Option } from "commander";
import type { Ctx } from "./commands/context.js";
import { deploy } from "./commands/deploy.js";
import { dev } from "./commands/dev.js";
import { envList, envSet, envUnset } from "./commands/env.js";
import { askInteractively, init, sshKeygen } from "./commands/init.js";
import { list } from "./commands/list.js";
import { logs } from "./commands/logs.js";
import { adminTarget, provision } from "./commands/provision.js";
import { remove } from "./commands/remove.js";
import { loadConfig } from "./lib/config.js";
import { exitOnBrokenPipe, report } from "./lib/report.js";
import { adminShell, sshRemote, targetFor } from "./lib/ssh.js";
import { VERSION } from "./version.js";

function ctx(): Ctx {
  const cwd = process.cwd();
  const config = loadConfig(cwd);
  return { cwd, config, remote: sshRemote(targetFor(config)), out: (s) => console.log(s) };
}

exitOnBrokenPipe(process.stdout);

const ask = (message: string) => confirm({ message, default: false });
const int = (v: string) => Number.parseInt(v, 10);

const program = new Command()
  .name("sagansync")
  .description("Deploy every git branch to its own HTTPS URL on your own VPS.")
  .version(VERSION);

program.command("init").description("configure this project for a VPS")
  .action(async () => {
    await init(process.cwd(), { ask: askInteractively, confirmOverwrite: () => ask("Overwrite the existing .sagansync/config.json?"), keygen: sshKeygen, out: (s) => console.log(s) });
  });

program.command("provision").description("install or upgrade sagand on the VPS (uses an admin account once)")
  .option("--admin <user@host>", "account with root or passwordless sudo (default: root@<host>)")
  .option("--admin-key <path>", "SSH key for the admin account")
  .option("--upgrade", "only replace the agent and restart it, keeping its settings")
  .option("--acme-email <email>", "email for Let's Encrypt expiry notices")
  .option("--remove-caddy", "remove Caddy left behind by an older SaganSync")
  .option("--agent-binary <path>", "install this sagand binary instead of downloading the release")
  .addOption(new Option("--acme-ca <url>", "ACME directory (tests)").hideHelp())
  .addOption(new Option("--acme-root-ca <path>", "extra CA for the ACME server (tests)").hideHelp())
  .action(async (o) => {
    const c = ctx();
    await provision(c, o, { shell: adminShell(adminTarget(c.config, o)) });
  });

program.command("deploy").description("build and release the current branch with zero downtime")
  .option("-w, --workspace <name>", "workspace to deploy (default: from the git branch)")
  .option("-v, --verbose", "show the full build output")
  .action((o) => deploy(ctx(), o));

program.command("dev").description("run the branch in dev mode and sync local edits live")
  .option("-w, --workspace <name>", "workspace to use (default: from the git branch)")
  .option("-c, --command <cmd>", "dev command run inside the container", "npm run dev")
  .option("--build", "rebuild the dev image")
  .option("--force", "allow dev mode on production")
  .option("-v, --verbose", "show the full build output")
  .action(async (o) => {
    const session = await dev(ctx(), { ...o, onStop: () => process.exit(1) });
    process.once("SIGINT", () => void session.close().then(() => process.exit(0)));
  });

program.command("list").description("show the workspaces of this project")
  .option("-a, --all", "show every project on the VPS")
  .action((o) => list(ctx(), o));

program.command("logs").description("show the logs of a workspace")
  .option("-w, --workspace <name>", "workspace (default: from the git branch)")
  .option("-n, --tail <lines>", "lines from the end", int, 100)
  .option("-f, --follow", "keep streaming new lines")
  .action((o) => logs(ctx(), o));

program.command("remove").description("delete a workspace and everything it uses")
  .option("-w, --workspace <name>", "workspace (default: from the git branch)")
  .option("-y, --yes", "do not ask for confirmation")
  .action((o) => remove(ctx(), { ...o, confirm: ask }));

const env = program.command("env").description("manage environment variables of a workspace");
env.command("set").description("set variables (applied on the next deploy)")
  .argument("[pairs...]", "KEY=VALUE pairs")
  .option("-f, --file <path>", "read KEY=VALUE lines from a file such as .env.production")
  .option("-w, --workspace <name>", "workspace (default: from the git branch)")
  .action((pairs: string[], o) => envSet(ctx(), pairs, o));
env.command("unset").description("remove variables (applied on the next deploy)")
  .argument("<keys...>", "variable names")
  .option("-w, --workspace <name>", "workspace (default: from the git branch)")
  .action((keys: string[], o) => envUnset(ctx(), keys, o));
env.command("list").description("list variable names (values are never shown)")
  .option("-w, --workspace <name>", "workspace (default: from the git branch)")
  .action((o) => envList(ctx(), o));

program.parseAsync(process.argv).catch((err: unknown) => {
  process.exitCode = report(err);
});

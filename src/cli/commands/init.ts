import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import readline from "node:readline";
import { spawn } from "node:child_process";
import chalk from "chalk";
import type { SaganSyncConfig } from "../utils/config";

type AskOptions = {
  defaultValue?: string;
  required?: boolean;
  validate?: (value: string) => string | true;
};

function expandHome(p: string): string {
  if (!p) return p;
  return p.startsWith("~") ? path.join(os.homedir(), p.slice(1)) : p;
}

function createAsk(rl: readline.Interface) {
  return (question: string, opts: AskOptions = {}): Promise<string> =>
    new Promise((resolve) => {
      const q = opts.defaultValue
        ? `${chalk.white.bold(question)} ${chalk.cyan.dim(`(${opts.defaultValue})`)}: `
        : `${chalk.white.bold(question)}: `;

      const loop = () => {
        rl.question(q, (raw) => {
          const value = (raw.trim() || opts.defaultValue || "").trim();

          if (opts.required && !value) {
            console.log(chalk.red("❌ This field is required."));
            return loop();
          }

          if (opts.validate) {
            const res = opts.validate(value);
            if (res !== true) {
              console.log(chalk.red(`❌ ${res}`));
              return loop();
            }
          }

          resolve(value);
        });
      };

      loop();
    });
}

function execSpawn(cmd: string, args: string[], opts: { cwd?: string } = {}): Promise<void> {
  return new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { stdio: "inherit", cwd: opts.cwd });
    child.on("exit", (code) => (code === 0 ? resolve() : reject(new Error(`${cmd} exited with code ${code}`))));
  });
}

function which(bin: string): Promise<boolean> {
  const cmd = process.platform === "win32" ? "where" : "which";
  return new Promise((resolve) => {
    const child = spawn(cmd, [bin], { stdio: "ignore" });
    child.on("exit", (code) => resolve(code === 0));
  });
}

function execCapture(
  cmd: string,
  args: string[],
  opts: { cwd?: string } = {}
): Promise<{ code: number; stdout: string; stderr: string }> {
  return new Promise((resolve) => {
    const child = spawn(cmd, args, { stdio: ["inherit", "pipe", "pipe"], cwd: opts.cwd });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (d: Buffer) => (stdout += d.toString()));
    child.stderr.on("data", (d: Buffer) => (stderr += d.toString()));
    child.on("exit", (code) => resolve({ code: code ?? 1, stdout, stderr }));
  });
}

async function askYesNo(
  ask: (q: string, o?: any) => Promise<string>,
  question: string,
  def: "Y" | "N" = "N"
): Promise<boolean> {
  const ans = (await ask(`${question} (${def === "Y" ? "Y/n" : "y/N"})`, { defaultValue: def })).trim();
  return def === "Y" ? !/^n(o)?$/i.test(ans) : /^y(es)?$/i.test(ans);
}

function shQuote(value: string): string {
  return `'${value.replace(/'/g, `'\"'\"'`)}'`;
}

export async function initCommand(): Promise<void> {
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
  const ask = createAsk(rl);

  const projectName = path.basename(process.cwd());
  const defaultRemotePath = `/srv/${projectName}`;

  const hostRaw = await ask("VPS host (user@ip)", {
    required: true,
    validate: (v) => (v.includes("@") ? true : "Expected format user@ip (e.g., ubuntu@1.2.3.4)"),
  });
  const [user, hostOnly] = hostRaw.split("@");

  const sshPortStr = await ask("SSH port", {
    defaultValue: "22",
    validate: (v) => (/^[0-9]+$/.test(v) ? true : "Must be a number"),
  });
  const sshPort = parseInt(sshPortStr, 10);

  const remotePath = await ask("Remote path on VPS", { defaultValue: defaultRemotePath, required: true });

  const internalPortStr = await ask("App internal port (inside container)", {
    defaultValue: "3000",
    required: true,
    validate: (v) => (/^[0-9]+$/.test(v) ? true : "Must be a number"),
  });
  const internalPort = parseInt(internalPortStr, 10);

  const domain = await ask("Domain (optional, e.g., api.example.com)", {
    validate: (v) => {
      if (!v) return true;
      if (v.startsWith("http://") || v.startsWith("https://")) return "Do not include protocol, only the hostname.";
      return /^[a-zA-Z0-9.-]+$/.test(v) ? true : "Invalid domain format";
    },
  });

  const createKeyAns = await ask("Create and install an SSH key for passwordless login now? (Y/n)", {
    defaultValue: "Y",
  });
  const willCreateKey = !/^n(o)?$/i.test(createKeyAns);

  const dir = path.join(process.cwd(), ".sagansync");
  const keysDir = path.join(dir, "keys");
  fs.mkdirSync(keysDir, { recursive: true });

  let identityFile: string | undefined;

  if (willCreateKey) {
    const keyName = `id_ed25519`;
    const keyPath = path.join(keysDir, keyName);
    const pubPath = `${keyPath}.pub`;

    if (fs.existsSync(keyPath) || fs.existsSync(pubPath)) {
      console.log(chalk.yellow("⚠️  SSH key already exists in .sagansync/keys/. Reusing it."));
    } else {
      const comment = `sagansync-${projectName}-${Date.now()}`;
      console.log(chalk.cyan("🔑 Generating SSH key (ed25519)..."));
      await execSpawn("ssh-keygen", ["-t", "ed25519", "-f", keyPath, "-N", "", "-C", comment]);
      try {
        fs.chmodSync(keyPath, 0o600);
      } catch {}
    }

    identityFile = keyPath;

    const hasCopyId = await which("ssh-copy-id");
    const target = `${user}@${hostOnly}`;

    if (hasCopyId) {
      console.log(
        chalk.cyan("📤 Installing public key on VPS via ssh-copy-id (you may be prompted for password)...")
      );
      const first = await execCapture("ssh-copy-id", ["-i", pubPath, "-p", String(sshPort), target]);
      const alreadyMsg = /All keys were skipped because they already exist/i.test(first.stdout + first.stderr);

      if (alreadyMsg) {
        console.log(chalk.yellow("⚠️  The key already exists on the remote server."));
        const force = await askYesNo(ask, "Force reinstall the key with ssh-copy-id -f?", "N");
        if (force) {
          const forced = await execCapture("ssh-copy-id", ["-f", "-i", pubPath, "-p", String(sshPort), target]);
          if (forced.code !== 0) {
            throw new Error(`ssh-copy-id -f failed:\n${forced.stderr || forced.stdout}`);
          }
          console.log(chalk.green("🔁 Key reinstalled with -f."));
        } else {
          console.log(chalk.gray("↩️  Keeping existing authorized_keys as-is."));
        }
      } else if (first.code !== 0) {
        throw new Error(`ssh-copy-id failed:\n${first.stderr || first.stdout}`);
      }
    } else {
      console.log(chalk.cyan("📤 Installing public key on VPS (fallback). You may be prompted for password..."));
      const pub = fs.readFileSync(pubPath, "utf8").trim();

      const force = await askYesNo(
        ask,
        "authorized_keys entry may already exist. Force reapply (dedupe + backup + add)?",
        "N"
      );

      const remoteCmd = force
        ? `bash -lc 'set -e; umask 077; mkdir -p ~/.ssh; touch ~/.ssh/authorized_keys; ` +
          `cp ~/.ssh/authorized_keys ~/.ssh/authorized_keys.bak.$(date +%s) || true; ` +
          `grep -vxF ${shQuote(pub)} ~/.ssh/authorized_keys > ~/.ssh/_authorized_keys.tmp || true; ` +
          `mv ~/.ssh/_authorized_keys.tmp ~/.ssh/authorized_keys; ` +
          `grep -qxF ${shQuote(pub)} ~/.ssh/authorized_keys || echo ${shQuote(pub)} >> ~/.ssh/authorized_keys; ` +
          `chmod 700 ~/.ssh; chmod 600 ~/.ssh/authorized_keys'`
        : `bash -lc 'set -e; umask 077; mkdir -p ~/.ssh; touch ~/.ssh/authorized_keys; ` +
          `grep -qxF ${shQuote(pub)} ~/.ssh/authorized_keys || echo ${shQuote(pub)} >> ~/.ssh/authorized_keys; ` +
          `chmod 700 ~/.ssh; chmod 600 ~/.ssh/authorized_keys'`;

      await execSpawn("ssh", [
        "-p",
        String(sshPort),
        "-o",
        "StrictHostKeyChecking=accept-new",
        `${target}`,
        remoteCmd,
      ]);
    }

    console.log(chalk.cyan("🧪 Testing key-based SSH login..."));
    await execSpawn("ssh", [
      "-i",
      identityFile,
      "-p",
      String(sshPort),
      "-o",
      "StrictHostKeyChecking=accept-new",
      `${user}@${hostOnly}`,
      "true",
    ]);

    console.log(chalk.green("✅ SSH key installed and working."));
  } else {
    const identityDefault =
      fs.existsSync(path.join(os.homedir(), ".ssh", "id_ed25519")) ? "~/.ssh/id_ed25519" : "~/.ssh/id_rsa";
    const identityFileInput = await ask("SSH identity file (or leave blank to use password every time)", {
      defaultValue: identityDefault,
    });
    identityFile = identityFileInput ? expandHome(identityFileInput) : undefined;
  }

  const cfg: SaganSyncConfig = {
    host: `${user}@${hostOnly}`,
    remotePath,
    internalPort,
    domain: domain?.trim() ? domain.trim() : undefined,
    ssh: {
      port: Number.isFinite(sshPort) ? sshPort : undefined,
      identityFile,
      user,
    },
  };

  const configPath = path.join(dir, "config.json");

  if (fs.existsSync(configPath)) {
    const overwrite = await ask("Config already exists. Overwrite? (y/N)", { defaultValue: "N" });
    if (!/^y(es)?$/i.test(overwrite)) {
      console.log(chalk.yellow("↩️  Keeping existing config. Aborted."));
      rl.close();
      return;
    }
  }

  const gi = path.join(dir, ".gitignore");
  if (!fs.existsSync(gi)) {
    fs.writeFileSync(
      gi,
      [
        "# Ignore SaganSync config and secrets",
        "config.json",
        "keys/",
        "*.secrets.json",
        "",
      ].join("\n")
    );
  }

  fs.writeFileSync(configPath, JSON.stringify(cfg, null, 2) + "\n");

  console.log(`${chalk.green("✅ Config saved to")} ${chalk.white.bold(".sagansync/config.json")}`);
  console.log(chalk.cyan(`Next steps:`));
  console.log(`  • ${chalk.white("sagansync provision")}  ${chalk.dim("# install Podman + Caddy on the VPS")}`);
  console.log(`  • ${chalk.white("sagansync deploy")}      ${chalk.dim("# build & run your container, wire domain")}`);

  rl.close();
}

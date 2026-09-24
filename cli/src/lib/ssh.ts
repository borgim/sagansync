import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import readline from "node:readline";
import type { Readable } from "node:stream";
import { type Config, configDir, keyPath, knownHostsPath } from "./config.js";

export type Target = {
  host: string;
  port: number;
  user: string;
  identityFile: string;
  knownHosts: string;
  controlDir: string;
};

export function targetFor(cfg: Config): Target {
  return { host: cfg.host, port: cfg.sshPort, user: cfg.user, identityFile: keyPath(cfg),
    knownHosts: knownHostsPath(), controlDir: configDir() };
}

// shQuote wraps s in single quotes for the remote shell. sagand's gateway
// splits SSH_ORIGINAL_COMMAND with the same rules, without any expansion.
export function shQuote(s: string): string {
  return `'${s.replaceAll("'", `'"'"'`)}'`;
}

export function remoteCommand(args: string[]): string {
  return ["sagand", ...args].map(shQuote).join(" ");
}

// ssh listens on "<ControlPath>.<16 random chars>" and %C expands to 40 hex
// characters. Unix socket paths are limited to 103 bytes on macOS, so long
// home directories fall back to a short per-user directory in /tmp.
const SOCKET_LIMIT = 103;

export function controlPath(t: Target): string {
  const preferred = path.join(t.controlDir, "cm-%C");
  if (preferred.length - 2 + 40 + 17 <= SOCKET_LIMIT) return preferred;
  const id = createHash("sha256").update(`${t.user}@${t.host}:${t.port}`).digest("hex").slice(0, 16);
  return path.join(`/tmp/sagansync-${process.getuid?.() ?? "user"}`, id);
}

// ensurePrivateDir creates dir (mode 0700) for the ssh control socket and
// refuses one that is a symlink, belongs to someone else, or that other users
// can write to (they could swap the socket). Read access is harmless: ssh
// creates the socket itself with mode 0600.
export function ensurePrivateDir(dir: string): void {
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const st = fs.lstatSync(dir);
  const mine = process.getuid === undefined || st.uid === process.getuid();
  if (!st.isDirectory() || !mine) {
    throw new Error(`${dir} must be a directory owned by you, since it holds the ssh connection socket. Move it aside and try again.`);
  }
  if ((st.mode & 0o022) !== 0) {
    throw new Error(`${dir} can be modified by other users, so it cannot hold the ssh connection socket. Fix it with: chmod 700 ${dir}`);
  }
}

export function sshArgs(t: Target): string[] {
  return [
    "-T",
    "-p", String(t.port),
    "-i", t.identityFile,
    "-l", t.user,
    "-o", "IdentitiesOnly=yes",
    "-o", "BatchMode=yes",
    "-o", "StrictHostKeyChecking=accept-new",
    "-o", `UserKnownHostsFile=${t.knownHosts}`,
    "-o", "ControlMaster=auto",
    "-o", `ControlPath=${controlPath(t)}`,
    "-o", "ControlPersist=60s",
    "-o", "LogLevel=ERROR",
    "--", t.host,
  ];
}

export type RunResult = { code: number; stdout: string; stderr: string };
export type Input = Readable | string | Buffer;

// Remote runs sagand subcommands on the VPS.
export interface Remote {
  run(args: string[], stdin?: Input): Promise<RunResult>;
  // stream calls onLine for every stdout line as it arrives.
  stream(args: string[], onLine: (line: string) => void, stdin?: Input): Promise<{ code: number; stderr: string }>;
}

// A Shell runs raw commands over ssh (used with the admin account).
export interface Shell {
  run(command: string, stdin?: Input): Promise<RunResult>;
  stream(command: string, onLine: (line: string) => void, stdin?: Input): Promise<{ code: number; stderr: string }>;
}

// sshShell spawns ssh with the given arguments followed by the command.
function sshShell(args: string[], sshBin: string, before: () => void = () => {}): Shell {
  const start = (command: string, stdin?: Input) => {
    before();
    const child = spawn(sshBin, [...args, command], { stdio: ["pipe", "pipe", "pipe"] });
    child.stdin.on("error", () => {}); // the remote may exit before reading all input
    // If the local input fails (unreadable file, packing error), ssh must not
    // see a clean EOF: the remote would accept the truncated body as complete.
    let inputError: Error | undefined;
    if (typeof stdin === "string" || Buffer.isBuffer(stdin)) child.stdin.end(stdin);
    else if (stdin) {
      stdin.on("error", (err) => {
        inputError = err;
        stdin.unpipe(child.stdin);
        child.kill("SIGTERM");
      });
      stdin.pipe(child.stdin);
    } else child.stdin.end();
    let stderr = "";
    child.stderr.setEncoding("utf8").on("data", (d: string) => (stderr += d));
    const done = new Promise<{ code: number; stderr: string }>((resolve, reject) => {
      child.on("error", reject);
      child.on("close", (code) => (inputError ? reject(inputError) : resolve({ code: code ?? 1, stderr })));
    });
    return { child, done };
  };
  return {
    async run(command, stdin) {
      const { child, done } = start(command, stdin);
      let stdout = "";
      child.stdout.setEncoding("utf8").on("data", (d: string) => (stdout += d));
      const { code, stderr } = await done;
      return { code, stdout, stderr };
    },
    async stream(command, onLine, stdin) {
      const { child, done } = start(command, stdin);
      const rl = readline.createInterface({ input: child.stdout, crlfDelay: Infinity });
      rl.on("line", onLine);
      const result = await done;
      rl.close();
      return result;
    },
  };
}

// sshRemote talks to sagand through ssh. SAGANSYNC_SSH replaces the ssh
// binary (tests use a fake one).
export function sshRemote(t: Target, sshBin = process.env.SAGANSYNC_SSH ?? "ssh"): Remote {
  const shell = sshShell(sshArgs(t), sshBin, () => ensurePrivateDir(path.dirname(controlPath(t))));
  return {
    run: (args, stdin) => shell.run(remoteCommand(args), stdin),
    stream: (args, onLine, stdin) => shell.stream(remoteCommand(args), onLine, stdin),
  };
}

// AdminTarget is the account `sagansync provision` installs with (root or a
// sudoer). Unlike the deploy key it may prompt for a passphrase or password,
// and it never reuses the deploy key's control socket.
export type AdminTarget = { host: string; port: number; user: string; identityFile?: string; knownHosts: string };

export function adminSshArgs(t: AdminTarget): string[] {
  return [
    "-T",
    "-p", String(t.port),
    ...(t.identityFile ? ["-i", t.identityFile, "-o", "IdentitiesOnly=yes"] : []),
    "-l", t.user,
    "-o", "StrictHostKeyChecking=accept-new",
    "-o", `UserKnownHostsFile=${t.knownHosts}`,
    "-o", "ControlMaster=no",
    "-o", "ControlPath=none",
    "-o", "LogLevel=ERROR",
    "--", t.host,
  ];
}

export function adminShell(t: AdminTarget, sshBin = process.env.SAGANSYNC_SSH ?? "ssh"): Shell {
  return sshShell(adminSshArgs(t), sshBin, () => fs.mkdirSync(path.dirname(t.knownHosts), { recursive: true, mode: 0o700 }));
}

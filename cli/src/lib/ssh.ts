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

// ensurePrivateDir creates dir (mode 0700) and refuses one that is a symlink,
// belongs to someone else, or is open to others, since it will hold the ssh
// control socket.
export function ensurePrivateDir(dir: string): void {
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const st = fs.lstatSync(dir);
  const mine = process.getuid === undefined || st.uid === process.getuid();
  if (!st.isDirectory() || !mine || (st.mode & 0o077) !== 0) {
    throw new Error(`${dir} is not private (it must be a directory owned by you with mode 0700); remove it and try again`);
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

// sshRemote talks to sagand through ssh. SAGANSYNC_SSH replaces the ssh
// binary (tests use a fake one).
export function sshRemote(t: Target, sshBin = process.env.SAGANSYNC_SSH ?? "ssh"): Remote {
  const start = (args: string[], stdin?: Input) => {
    ensurePrivateDir(path.dirname(controlPath(t)));
    const child = spawn(sshBin, [...sshArgs(t), remoteCommand(args)], { stdio: ["pipe", "pipe", "pipe"] });
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
    async run(args, stdin) {
      const { child, done } = start(args, stdin);
      let stdout = "";
      child.stdout.setEncoding("utf8").on("data", (d: string) => (stdout += d));
      const { code, stderr } = await done;
      return { code, stdout, stderr };
    },
    async stream(args, onLine, stdin) {
      const { child, done } = start(args, stdin);
      const rl = readline.createInterface({ input: child.stdout, crlfDelay: Infinity });
      rl.on("line", onLine);
      const result = await done;
      rl.close();
      return result;
    },
  };
}

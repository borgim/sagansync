import { spawn } from "node:child_process";
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
    "-o", `ControlPath=${path.join(t.controlDir, "cm-%C")}`,
    "-o", "ControlPersist=60s",
    "-o", "LogLevel=ERROR",
    "--", t.host,
  ];
}

export type RunResult = { code: number; stdout: string; stderr: string };
export type Input = Readable | string;

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
    fs.mkdirSync(t.controlDir, { recursive: true, mode: 0o700 });
    const child = spawn(sshBin, [...sshArgs(t), remoteCommand(args)], { stdio: ["pipe", "pipe", "pipe"] });
    child.stdin.on("error", () => {}); // the remote may exit before reading all input
    if (typeof stdin === "string") child.stdin.end(stdin);
    else if (stdin) {
      stdin.on("error", (err) => child.stdin.destroy(err));
      stdin.pipe(child.stdin);
    } else child.stdin.end();
    let stderr = "";
    child.stderr.setEncoding("utf8").on("data", (d: string) => (stderr += d));
    const done = new Promise<{ code: number; stderr: string }>((resolve, reject) => {
      child.on("error", reject);
      child.on("close", (code) => resolve({ code: code ?? 1, stderr }));
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

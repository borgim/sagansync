import type { Config } from "./config.js";
import { type AgentEvent, exitCodeFor, HINTS, parseEvent, Renderer } from "./events.js";
import type { Input, Remote, RunResult } from "./ssh.js";
import { PROTOCOL, VERSION } from "../version.js";

// CliError is a failure to show the user without a stack trace.
export class CliError extends Error {
  constructor(
    message: string,
    readonly exitCode = 1,
    readonly hint?: string,
    readonly details: string[] = [],
  ) {
    super(message);
  }
}

export function eventError(e: AgentEvent): CliError {
  const details = e.logs && e.logs.length > 0 ? ["Last container logs:", ...e.logs.map((l) => `  ${l}`)] : [];
  return new CliError(e.message ?? e.code ?? "unknown error", exitCodeFor(e), HINTS[e.code ?? ""], details);
}

// ssh exits with 255 when the connection itself fails.
export function sshError(stderr: string): CliError {
  const text = stderr.trim();
  let hint = "Check that the VPS is reachable and that `sagansync provision` has been run.";
  if (text.includes("REMOTE HOST IDENTIFICATION HAS CHANGED")) {
    hint = "The VPS host key changed. If you reinstalled the server, delete its line from ~/.config/sagansync/known_hosts.";
  } else if (text.includes("Permission denied")) {
    hint = "The deploy key is not authorized on the VPS. Run `sagansync provision`.";
  }
  return new CliError(`Could not connect over SSH: ${text || "unknown error"}`, 1, hint);
}

// failure converts a non-zero RunResult into a CliError.
export function failure(r: RunResult): CliError {
  if (r.code === 255) return sshError(r.stderr);
  const lines = r.stdout.trim().split("\n");
  const e = parseEvent(lines[lines.length - 1] ?? "");
  if (e?.type === "error") return eventError(e);
  if (r.code === 126) return new CliError(`The VPS refused the command: ${r.stderr.trim()}`, 1, "The agent on the VPS may be older than this CLI. Run `sagansync provision --upgrade`.");
  return new CliError(`sagand failed (exit ${r.code}): ${(r.stderr || r.stdout).trim()}`);
}

// checkAgent makes sure sagand answers and speaks our protocol. It returns a
// warning when the versions differ but are compatible.
export async function checkAgent(remote: Remote): Promise<string | undefined> {
  const r = await remote.run(["version"]);
  if (r.code !== 0) throw failure(r);
  let info: { version?: string; protocol?: number };
  try {
    info = JSON.parse(r.stdout);
  } catch {
    throw new CliError("sagand gave an unexpected answer to `version`.", 1, "Run `sagansync provision --upgrade`.");
  }
  if (info.protocol !== PROTOCOL) {
    throw new CliError(`sagand on the VPS speaks protocol ${info.protocol}, this CLI speaks ${PROTOCOL}.`, 1,
      "Run `sagansync provision --upgrade` to install the matching agent.");
  }
  if (info.version !== VERSION) {
    return `sagand ${info.version} is running on the VPS and this CLI is ${VERSION}. Run \`sagansync provision --upgrade\` to align them.`;
  }
  return undefined;
}

// runStreaming runs an event-producing sagand command, renders its progress
// and returns the final done event, or throws on error.
export async function runStreaming(remote: Remote, args: string[], opts: { out: (s: string) => void; verbose?: boolean; stdin?: Input }): Promise<AgentEvent> {
  const renderer = new Renderer(opts.out, opts.verbose);
  const { code, stderr } = await remote.stream(args, (l) => renderer.line(l), opts.stdin);
  const last = renderer.last;
  if (last?.type === "error") throw eventError(last);
  if (last?.type === "done") return last;
  throw failure({ code, stdout: "", stderr });
}

export function workspaceFlags(cfg: Config, workspace: string): string[] {
  return ["--project", cfg.project, "--workspace", workspace];
}

export function domainFlags(cfg: Config): string[] {
  const f: string[] = [];
  if (cfg.domain) f.push("--domain", cfg.domain);
  if (cfg.previewDomain) f.push("--preview-domain", cfg.previewDomain);
  return f;
}

// requestFlags are the flags `sagand deploy` and `sagand dev` share.
export function requestFlags(cfg: Config, workspace: string, sha: string): string[] {
  const f = [...workspaceFlags(cfg, workspace), "--port", String(cfg.internalPort), ...domainFlags(cfg)];
  if (cfg.healthPath) f.push("--health-path", cfg.healthPath);
  if (cfg.healthTimeout) f.push("--health-timeout", String(cfg.healthTimeout));
  if (sha) f.push("--sha", sha);
  return f;
}

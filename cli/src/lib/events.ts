import { paint } from "./style.js";

// AgentEvent is one line of sagand's NDJSON output (agent/internal/events).
export type AgentEvent = {
  v: number;
  type: "step" | "log" | "warn" | "done" | "error";
  name?: string;
  stream?: string;
  line?: string;
  url?: string;
  release?: string;
  hostPort?: number;
  code?: string;
  message?: string;
  logs?: string[];
};

const TYPES = new Set(["step", "log", "warn", "done", "error"]);

// parseEvent returns the event on a line, or null for anything else
// (application logs may be JSON too, but they never carry "v" and our types).
export function parseEvent(line: string): AgentEvent | null {
  if (!line.startsWith("{")) return null;
  try {
    const e = JSON.parse(line) as Partial<AgentEvent>;
    if (typeof e.v === "number" && typeof e.type === "string" && TYPES.has(e.type)) return e as AgentEvent;
  } catch {
    // not an event
  }
  return null;
}

const STEPS: Record<string, string> = {
  extract: "Unpacking upload",
  build: "Building image",
  start: "Starting container",
  health: "Waiting for the health check",
  tls: "Getting the TLS certificate",
  drain: "Retiring the previous release",
};

// Renderer prints progress for a stream of events and remembers the final
// done or error event.
export class Renderer {
  last: AgentEvent | null = null;

  constructor(
    private readonly out: (s: string) => void,
    private readonly verbose = false,
  ) {}

  line(raw: string): void {
    const e = parseEvent(raw);
    if (!e) {
      if (raw.trim()) this.out(raw);
      return;
    }
    switch (e.type) {
      case "step":
        this.out(`${paint("cyan", "▸")} ${STEPS[e.name ?? ""] ?? e.name}`);
        break;
      case "log":
        if (this.verbose) this.out(paint("gray", `  ${e.line ?? ""}`));
        break;
      case "warn":
        this.out(paint("yellow", `! ${e.message ?? e.code}`));
        break;
      case "done":
      case "error":
        this.last = e;
        break;
    }
  }
}

export const HINTS: Record<string, string> = {
  busy: "Another operation is running on this workspace. Wait for it to finish and try again.",
  build_failed: "The image build failed. Run again with --verbose to see the full build output.",
  health_failed: "The new release never became healthy, so the previous one is still serving traffic.",
  invalid_archive: "The upload was rejected. If the connection dropped, just run the command again.",
  host_conflict: "Another project already uses this hostname. Change domain or previewDomain in .sagansync/config.json.",
  production_locked: "Use a feature branch, or pass --force if you really mean to run dev mode on production.",
  not_found: "Run `sagansync list` to see the workspaces on this VPS.",
  daemon_unavailable: "The sagand service is not running on the VPS. Run `sagansync provision --upgrade` to repair it.",
};

// exitCodeFor mirrors sagand's events.ExitCode.
export function exitCodeFor(e: AgentEvent): number {
  if (e.type === "done") return 0;
  return e.code === "invalid" ? 2 : 1;
}

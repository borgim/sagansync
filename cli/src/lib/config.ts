import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export type Config = {
  host: string;
  sshPort: number;
  user: string;
  project: string;
  internalPort: number;
  domain?: string;
  previewDomain?: string;
  healthPath?: string;
  healthTimeout?: number;
  identityFile?: string;
};

export class ConfigError extends Error {}

// Same rules sagand enforces (agent/internal/validate).
export const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/;
const LABEL_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
// Hostnames, IPv4 and IPv6. Must not start with "-" so ssh never reads it as an option.
const HOST_RE = /^[A-Za-z0-9][A-Za-z0-9.:-]*$/;
const USER_RE = /^[a-z_][a-z0-9_-]{0,31}$/;

export function isDomain(s: string): boolean {
  if (s.length === 0 || s.length > 253) return false;
  const labels = s.split(".");
  return labels.length >= 2 && labels.every((l) => LABEL_RE.test(l));
}

function isPort(n: unknown): n is number {
  return Number.isInteger(n) && (n as number) >= 1 && (n as number) <= 65535;
}

// validateConfig checks a parsed config.json and fills in defaults.
export function validateConfig(raw: unknown): Config {
  if (typeof raw !== "object" || raw === null) throw new ConfigError("config must be a JSON object");
  const c = raw as Record<string, unknown>;
  const problems: string[] = [];
  const str = (k: string) => (typeof c[k] === "string" ? (c[k] as string) : undefined);

  const host = str("host");
  if (!host || !HOST_RE.test(host)) problems.push("host must be a hostname or IP address (without user@)");
  const sshPort = c.sshPort ?? 22;
  if (!isPort(sshPort)) problems.push("sshPort must be a port number");
  const user = str("user") ?? "sagan";
  if (!USER_RE.test(user)) problems.push("user must be a valid Unix user name");
  const project = str("project");
  if (!project || !NAME_RE.test(project)) problems.push("project must be 1-40 characters of a-z, 0-9 and '-'");
  if (!isPort(c.internalPort)) problems.push("internalPort must be a port number");
  for (const k of ["domain", "previewDomain"]) {
    if (c[k] !== undefined && !(typeof c[k] === "string" && isDomain(c[k] as string))) {
      problems.push(`${k} must be a hostname like example.com, without protocol or port`);
    }
  }
  const healthPath = str("healthPath");
  if (c.healthPath !== undefined && !(healthPath && /^\/\S{0,199}$/.test(healthPath))) {
    problems.push("healthPath must start with '/' and contain no spaces");
  }
  const t = c.healthTimeout;
  if (t !== undefined && !(Number.isInteger(t) && (t as number) >= 1 && (t as number) <= 600)) {
    problems.push("healthTimeout must be 1-600 seconds");
  }
  if (c.identityFile !== undefined && typeof c.identityFile !== "string") problems.push("identityFile must be a path");
  if (problems.length > 0) throw new ConfigError(`Invalid .sagansync/config.json:\n  - ${problems.join("\n  - ")}`);

  const cfg: Config = { host: host!, sshPort: sshPort as number, user, project: project!, internalPort: c.internalPort as number };
  if (c.domain !== undefined) cfg.domain = c.domain as string;
  if (c.previewDomain !== undefined) cfg.previewDomain = c.previewDomain as string;
  if (healthPath !== undefined) cfg.healthPath = healthPath;
  if (t !== undefined) cfg.healthTimeout = t as number;
  if (c.identityFile !== undefined) cfg.identityFile = c.identityFile as string;
  return cfg;
}

export function configPath(cwd: string): string {
  return path.join(cwd, ".sagansync", "config.json");
}

export function loadConfig(cwd: string): Config {
  let text: string;
  try {
    text = fs.readFileSync(configPath(cwd), "utf8");
  } catch {
    throw new ConfigError("No .sagansync/config.json in this directory. Run `sagansync init` first.");
  }
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (err) {
    throw new ConfigError(`.sagansync/config.json is not valid JSON: ${(err as Error).message}`);
  }
  return validateConfig(raw);
}

export function saveConfig(cwd: string, cfg: Config): void {
  fs.mkdirSync(path.dirname(configPath(cwd)), { recursive: true });
  fs.writeFileSync(configPath(cwd), JSON.stringify(cfg, null, 2) + "\n");
}

// configDir holds keys, known_hosts and SSH control sockets.
// SAGANSYNC_HOME overrides it (used by tests).
export function configDir(): string {
  return process.env.SAGANSYNC_HOME ?? path.join(os.homedir(), ".config", "sagansync");
}

export function keyPath(cfg: Pick<Config, "host" | "identityFile">): string {
  return cfg.identityFile ?? path.join(configDir(), "keys", `${cfg.host}_ed25519`);
}

export function knownHostsPath(): string {
  return path.join(configDir(), "known_hosts");
}

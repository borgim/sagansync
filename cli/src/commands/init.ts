import { execFileSync } from "node:child_process";
import fs from "node:fs";
import { isIP } from "node:net";
import path from "node:path";
import { input } from "@inquirer/prompts";
import { type Config, configPath, isDomain, keyPath, NAME_RE, saveConfig, validateConfig } from "../lib/config.js";
import { dnsRecords } from "../lib/dns.js";
import { paint } from "../lib/style.js";

export type InitAnswers = {
  host: string;
  sshPort: number;
  project: string;
  internalPort: number;
  domain?: string;
  previewDomain?: string;
  healthPath?: string;
};

export type InitDeps = {
  ask: (defaults: { project: string }) => Promise<InitAnswers>;
  confirmOverwrite: () => Promise<boolean>;
  keygen: (keyFile: string, comment: string) => void;
  out: (line: string) => void;
};

export function defaultProject(cwd: string): string {
  const name = path.basename(cwd).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40).replace(/-+$/, "");
  return NAME_RE.test(name) ? name : "app";
}

// init writes .sagansync/config.json, creates the deploy key if needed and
// explains the DNS records and next steps.
export async function init(cwd: string, deps: InitDeps): Promise<Config | null> {
  if (fs.existsSync(configPath(cwd)) && !(await deps.confirmOverwrite())) {
    deps.out("Kept the existing .sagansync/config.json.");
    return null;
  }
  const cfg = validateConfig({ ...(await deps.ask({ project: defaultProject(cwd) })), user: "sagan" });
  saveConfig(cwd, cfg);
  deps.out(paint("green", "✔ Saved .sagansync/config.json") + " (no secrets inside, safe to commit)");

  const key = keyPath(cfg);
  if (fs.existsSync(key)) {
    deps.out(`Using the existing deploy key ${key}`);
  } else {
    fs.mkdirSync(path.dirname(key), { recursive: true, mode: 0o700 });
    deps.keygen(key, `sagansync-${cfg.project}`);
    deps.out(paint("green", `✔ Created deploy key ${key}`));
  }

  const records = dnsRecords(cfg, isIP(cfg.host) ? cfg.host : "<VPS IP>");
  if (records.length > 0) {
    deps.out("\nCreate these DNS records:");
    for (const r of records) deps.out(`  ${r}`);
  }
  deps.out("\nNext steps:");
  deps.out(`  sagansync provision --admin root@${cfg.host}   # installs sagand on the VPS`);
  deps.out("  sagansync deploy");
  return cfg;
}

export function sshKeygen(keyFile: string, comment: string): void {
  execFileSync("ssh-keygen", ["-q", "-t", "ed25519", "-N", "", "-C", comment, "-f", keyFile], { stdio: "inherit" });
}

const optional = (v: string) => (v.trim() === "" ? undefined : v.trim());
const port = (v: string) => (/^\d+$/.test(v) && +v >= 1 && +v <= 65535 ? true : "Enter a port number (1-65535)");
const domain = (v: string) => (v.trim() === "" || isDomain(v.trim()) ? true : "Enter a hostname like example.com, without https:// or a port");

export async function askInteractively(defaults: { project: string }): Promise<InitAnswers> {
  const host = await input({ message: "VPS address (hostname or IP)", validate: (v) => (/^[A-Za-z0-9][A-Za-z0-9.:-]*$/.test(v) ? true : "Enter just the hostname or IP, without user@") });
  const sshPort = await input({ message: "SSH port", default: "22", validate: port });
  const project = await input({ message: "Project name", default: defaults.project, validate: (v) => (NAME_RE.test(v) ? true : "Use 1-40 characters of a-z, 0-9 and '-'") });
  const internalPort = await input({ message: "Port your app listens on inside the container", default: "3000", validate: port });
  const dom = await input({ message: "Production domain (optional, e.g. api.example.com)", validate: domain });
  const preview = await input({ message: "Domain for branch previews (optional, e.g. example.com gives feat-x-app.example.com)", validate: domain });
  const health = await input({ message: "Health check path (optional, e.g. /health)", validate: (v) => (v === "" || /^\/\S*$/.test(v) ? true : "Start with / and use no spaces") });
  return { host, sshPort: +sshPort, project, internalPort: +internalPort, domain: optional(dom), previewDomain: optional(preview), healthPath: optional(health) };
}

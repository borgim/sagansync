import fs from "node:fs";
import path from "node:path";

export type SaganSyncConfig = {
  host: string;
  remotePath: string;
  internalPort: number;
  domain?: string;
  projectType?: string;
  ssh?: {
    port?: number;
    identityFile?: string;
    user?: string;
  };
};

export function loadConfig(cwd: string = process.cwd()): SaganSyncConfig {
  const configPath = path.join(cwd, ".sagansync", "config.json");
  if (!fs.existsSync(configPath)) {
    throw new Error(`Missing config at ${configPath}. Run 'sagansync init' first.`);
  }
  const raw = fs.readFileSync(configPath, "utf8");
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    throw new Error(`Invalid JSON in ${configPath}`);
  }

  const cfg = parsed as Partial<SaganSyncConfig>;
  if (!cfg.host || !cfg.remotePath || typeof cfg.internalPort !== "number") {
    throw new Error("Config must include host, remotePath, and internalPort (number)");
  }

  return cfg as SaganSyncConfig;
}



import { promises as dns } from "node:dns";
import type { Config } from "./config.js";

export type Lookup = (host: string) => Promise<string[]>;

export const systemLookup: Lookup = async (host) => (await dns.lookup(host, { all: true })).map((a) => a.address);

// dnsWarning explains what to fix when publicHost does not resolve to the VPS.
// It returns null when everything matches or when the VPS itself cannot be
// resolved (nothing useful to compare against).
export async function dnsWarning(publicHost: string, vpsHost: string, lookup: Lookup = systemLookup): Promise<string | null> {
  let vps: string[];
  try {
    vps = await lookup(vpsHost);
  } catch {
    return null;
  }
  let pub: string[];
  try {
    pub = await lookup(publicHost);
  } catch {
    return `${publicHost} does not resolve yet. Create a DNS record pointing it to ${vps[0]}, or HTTPS will not work.`;
  }
  if (pub.some((a) => vps.includes(a))) return null;
  return `${publicHost} resolves to ${pub.join(", ")}, but the VPS is ${vps.join(", ")}. Point it to the VPS, or HTTPS will not work.`;
}

// dnsRecords lists the records a project needs, for `sagansync init`.
export function dnsRecords(cfg: Pick<Config, "domain" | "previewDomain">, address: string): string[] {
  const records: string[] = [];
  if (cfg.domain) records.push(`A  ${cfg.domain}  ->  ${address}`);
  if (cfg.previewDomain) records.push(`A  *.${cfg.previewDomain}  ->  ${address}   (one wildcard for every project's branches)`);
  else if (cfg.domain) records.push(`A  *.${cfg.domain}  ->  ${address}   (only needed for branch workspaces)`);
  return records;
}

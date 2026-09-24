import { checkAgent, domainFlags, workspaceFlags } from "../lib/agent.js";
import type { Config } from "../lib/config.js";
import { dnsWarning, type Lookup } from "../lib/dns.js";
import { listFiles, type FileList } from "../lib/pack.js";
import type { Remote } from "../lib/ssh.js";
import { paint } from "../lib/style.js";

// Ctx is everything a command needs; tests build it with fakes.
export type Ctx = {
  cwd: string;
  config: Config;
  remote: Remote;
  out: (line: string) => void;
  lookup?: Lookup;
};

export function warn(ctx: Ctx, message: string): void {
  ctx.out(paint("yellow", `! ${message}`));
}

// preflight checks that the agent answers and speaks our protocol.
export async function preflight(ctx: Ctx): Promise<void> {
  const warning = await checkAgent(ctx.remote);
  if (warning) warn(ctx, warning);
}

// checkDns warns (without failing) when the workspace hostname does not point
// to the VPS. The hostname comes from sagand so the rule lives in one place.
export async function checkDns(ctx: Ctx, workspace: string): Promise<void> {
  if (!ctx.config.domain && !ctx.config.previewDomain) return;
  const r = await ctx.remote.run(["host", ...workspaceFlags(ctx.config, workspace), ...domainFlags(ctx.config)]);
  if (r.code !== 0) return;
  let host = "";
  try {
    host = (JSON.parse(r.stdout) as { host?: string }).host ?? "";
  } catch {
    return;
  }
  if (!host) return;
  const w = await dnsWarning(host, ctx.config.host, ctx.lookup);
  if (w) warn(ctx, w);
}

// collect lists the project files and reports skipped symlinks.
export function collect(ctx: Ctx): FileList {
  const files = listFiles(ctx.cwd);
  for (const s of files.skipped) warn(ctx, `Skipping symlink that points outside the project: ${s}`);
  return files;
}

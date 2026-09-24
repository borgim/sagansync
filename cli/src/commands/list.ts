import { failure } from "../lib/agent.js";
import { type Ctx, preflight } from "./context.js";

type Status = { project: string; workspace: string; mode: string; release: string; url?: string; hostPort: number; running: boolean };

export function formatTable(headers: string[], rows: string[][]): string[] {
  const widths = headers.map((h, i) => Math.max(h.length, ...rows.map((r) => (r[i] ?? "").length)));
  const fmt = (cells: string[]) => cells.map((c, i) => c.padEnd(widths[i] ?? 0)).join("  ").trimEnd();
  return [fmt(headers), ...rows.map(fmt)];
}

export async function list(ctx: Ctx, opts: { all?: boolean }): Promise<void> {
  await preflight(ctx);
  const r = await ctx.remote.run(["list"]);
  if (r.code !== 0) throw failure(r);
  const all = JSON.parse(r.stdout) as Status[];
  const rows = opts.all ? all : all.filter((s) => s.project === ctx.config.project);
  if (rows.length === 0) {
    ctx.out(opts.all ? "Nothing is deployed on this VPS yet." : `Nothing deployed for ${ctx.config.project} yet. Run \`sagansync deploy\`.`);
    return;
  }
  const headers = opts.all ? ["PROJECT", "WORKSPACE", "MODE", "STATUS", "RELEASE", "URL"] : ["WORKSPACE", "MODE", "STATUS", "RELEASE", "URL"];
  const table = rows.map((s) => {
    const cells = [s.workspace, s.mode, s.running ? "running" : "stopped", s.release, s.url ?? `127.0.0.1:${s.hostPort}`];
    return opts.all ? [s.project, ...cells] : cells;
  });
  for (const line of formatTable(headers, table)) ctx.out(line);
}

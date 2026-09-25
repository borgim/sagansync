import { CliError, eventError, failure, workspaceFlags } from "../lib/agent.js";
import { type AgentEvent, parseEvent } from "../lib/events.js";
import { resolveWorkspace } from "../lib/git.js";
import { type Ctx, preflight } from "./context.js";

export type LogsOptions = { workspace?: string; tail?: number; follow?: boolean };

export async function logs(ctx: Ctx, opts: LogsOptions): Promise<void> {
  const tail = opts.tail ?? 100;
  if (!Number.isInteger(tail) || tail < 0) throw new CliError("--tail must be a whole number of lines.", 2);
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  await preflight(ctx);
  const args = ["logs", ...workspaceFlags(ctx.config, workspace), "--tail", String(tail)];
  if (opts.follow) args.push("-f");
  let error: AgentEvent | null = null;
  const { code, stderr } = await ctx.remote.stream(args, (line) => {
    const e = parseEvent(line);
    if (e?.type === "error") error = e;
    else ctx.out(line);
  });
  if (error) throw eventError(error);
  if (code !== 0) throw failure({ code, stdout: "", stderr });
}

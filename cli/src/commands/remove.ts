import { runStreaming, workspaceFlags } from "../lib/agent.js";
import { resolveWorkspace } from "../lib/git.js";
import { paint } from "../lib/style.js";
import { type Ctx, preflight } from "./context.js";

export type RemoveOptions = { workspace?: string; yes?: boolean; confirm: (message: string) => Promise<boolean> };

export async function remove(ctx: Ctx, opts: RemoveOptions): Promise<void> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  const what = `${ctx.config.project}/${workspace}`;
  if (!opts.yes && !(await opts.confirm(`Remove ${what}? Its container, releases, files and env vars will be deleted.`))) {
    ctx.out("Cancelled.");
    return;
  }
  await preflight(ctx);
  await runStreaming(ctx.remote, ["remove", ...workspaceFlags(ctx.config, workspace)], { out: ctx.out });
  ctx.out(paint("green", `✔ Removed ${what}`));
}

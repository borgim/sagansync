import { requestFlags, runStreaming } from "../lib/agent.js";
import { currentSha, resolveWorkspace } from "../lib/git.js";
import { packProject } from "../lib/pack.js";
import { paint } from "../lib/style.js";
import { checkDns, collect, type Ctx, preflight } from "./context.js";

export type DeployOptions = { workspace?: string; verbose?: boolean };

export async function deploy(ctx: Ctx, opts: DeployOptions): Promise<void> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  await preflight(ctx);
  await checkDns(ctx, workspace);
  const { entries } = collect(ctx);
  ctx.out(`Deploying ${ctx.config.project}/${workspace} (${entries.filter((e) => e.type === "file").length} files)`);
  const done = await runStreaming(ctx.remote, ["deploy", ...requestFlags(ctx.config, workspace, currentSha(ctx.cwd))], {
    out: ctx.out, verbose: opts.verbose, stdin: packProject(entries),
  });
  ctx.out(paint("green", done.url ? `✔ Live at ${done.url}` : `✔ Running on port ${done.hostPort} of the VPS (no domain configured)`));
}

import fs from "node:fs";
import path from "node:path";
import chokidar from "chokidar";
import { CliError, failure, requestFlags, runStreaming } from "../lib/agent.js";
import { HINTS } from "../lib/events.js";
import { branchState, currentSha, resolveWorkspace } from "../lib/git.js";
import { ignoreMatcher, packProject } from "../lib/pack.js";
import type { RunResult } from "../lib/ssh.js";
import { paint } from "../lib/style.js";
import { checkDns, collect, type Ctx, preflight } from "./context.js";

export type DevOptions = {
  workspace?: string;
  command?: string;
  build?: boolean;
  force?: boolean;
  verbose?: boolean;
  onBranchChange?: () => void;
};

export type Syncer = { put(rel: string): Promise<void>; rm(rel: string): Promise<void>; idle(): Promise<void> };

// createSyncer sends file changes to the dev workspace one at a time, in the
// order they happened. A failed sync is reported and does not stop the queue.
export function createSyncer(ctx: Ctx, workspace: string): Syncer {
  let chain: Promise<void> = Promise.resolve();
  const enqueue = (label: string, op: () => Promise<RunResult>) => {
    chain = chain.then(async () => {
      try {
        const r = await op();
        if (r.code !== 0) throw failure(r);
        ctx.out(paint("gray", `  ${label}`));
      } catch (err) {
        ctx.out(paint("red", `  ✖ ${label}: ${(err as Error).message}`));
      }
    });
    return chain;
  };
  const { project } = ctx.config;
  return {
    put: (rel) => enqueue(`↑ ${rel}`, () => ctx.remote.run(["put", project, workspace, rel], fs.createReadStream(path.join(ctx.cwd, rel)))),
    rm: (rel) => enqueue(`✕ ${rel}`, () => ctx.remote.run(["rm", project, workspace, rel])),
    idle: () => chain,
  };
}

export type DevSession = { close(): Promise<void>; idle(): Promise<void> };

// dev starts the workspace in dev mode and keeps it in sync with local edits.
// It stops if the git branch changes, so one branch never writes into
// another branch's workspace.
export async function dev(ctx: Ctx, opts: DevOptions): Promise<DevSession> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  if (workspace === "production" && !opts.force) {
    throw new CliError("Dev mode is disabled on the production workspace.", 1, HINTS.production_locked);
  }
  await preflight(ctx);
  await checkDns(ctx, workspace);
  const { entries } = collect(ctx);
  const args = ["dev", ...requestFlags(ctx.config, workspace, currentSha(ctx.cwd)),
    "--command", JSON.stringify(["sh", "-c", opts.command ?? "npm run dev"])];
  if (opts.build) args.push("--build");
  if (opts.force) args.push("--force");
  const done = await runStreaming(ctx.remote, args, { out: ctx.out, verbose: opts.verbose, stdin: packProject(entries) });
  ctx.out(paint("green", done.url ? `✔ Dev server at ${done.url}` : `✔ Dev server on port ${done.hostPort} of the VPS`));

  const syncer = createSyncer(ctx, workspace);
  const match = ignoreMatcher(ctx.cwd);
  const rel = (p: string) => path.relative(ctx.cwd, p).split(path.sep).join("/");
  const watcher = chokidar.watch(ctx.cwd, {
    ignoreInitial: true,
    ignored: (p, stats) => {
      const r = rel(p);
      return r !== "" && match(r, stats?.isDirectory() ?? false);
    },
    awaitWriteFinish: { stabilityThreshold: 100, pollInterval: 25 },
  });
  watcher
    .on("add", (p) => void syncer.put(rel(p)))
    .on("change", (p) => void syncer.put(rel(p)))
    .on("unlink", (p) => void syncer.rm(rel(p)))
    .on("unlinkDir", (p) => void syncer.rm(rel(p)));
  await new Promise<void>((resolve) => watcher.once("ready", () => resolve()));

  // git replaces .git/HEAD with a rename on checkout, which file watchers miss,
  // so the branch is polled instead.
  const start = JSON.stringify(branchState(ctx.cwd));
  let stopped = false;
  const close = async () => {
    stopped = true;
    clearInterval(timer);
    await watcher.close();
  };
  const timer = setInterval(() => {
    if (stopped || JSON.stringify(branchState(ctx.cwd)) === start) return;
    ctx.out(paint("red", `✖ The git branch changed. Stopped syncing to ${workspace} so it does not receive another branch's files.`));
    void close().then(() => opts.onBranchChange?.());
  }, 1000);
  ctx.out("Watching for changes (Ctrl+C to stop)...");
  return { close, idle: () => syncer.idle() };
}

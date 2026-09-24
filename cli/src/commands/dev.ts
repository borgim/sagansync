import fs from "node:fs";
import path from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import chokidar from "chokidar";
import { CliError, failure, requestFlags, runStreaming } from "../lib/agent.js";
import { HINTS } from "../lib/events.js";
import { branchState, currentSha, gitPath, resolveWorkspace } from "../lib/git.js";
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
  onStop?: () => void; // the session stopped by itself (branch change, watcher failure)
  watch?: typeof chokidar.watch; // tests use it to reach the watcher
};

export type Syncer = { put(rel: string): Promise<void>; rm(rel: string): Promise<void>; idle(): Promise<void>; stop(): void };

// createSyncer sends file changes to the dev workspace one at a time, in the
// order they happened. A failed sync is reported and does not stop the queue.
// When a guard is given, it runs right before each send; if it returns false
// the queue is dropped and nothing else is sent.
export function createSyncer(ctx: Ctx, workspace: string, guard?: () => Promise<boolean>): Syncer {
  let chain: Promise<void> = Promise.resolve();
  let stopped = false;
  const enqueue = (label: string, op: () => Promise<RunResult>) => {
    chain = chain.then(async () => {
      if (stopped) return;
      if (guard && !(await guard())) {
        stopped = true;
        return;
      }
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
    // The whole file is read first so a read error never reaches the remote.
    put: (rel) => enqueue(`↑ ${rel}`, async () => ctx.remote.run(["put", project, workspace, rel], await fs.promises.readFile(path.join(ctx.cwd, rel)))),
    rm: (rel) => enqueue(`✕ ${rel}`, () => ctx.remote.run(["rm", project, workspace, rel])),
    idle: () => chain,
    stop: () => {
      stopped = true;
    },
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

  // During a checkout git rewrites the working tree first and HEAD last, so
  // file events arrive while HEAD still names the old branch. Before each send
  // wait for any git operation holding index.lock (a plain commit passes
  // quickly), then compare the branch; on a switch, drop the queue and stop.
  const start = JSON.stringify(branchState(ctx.cwd));
  const lock = gitPath(ctx.cwd, "index.lock");
  const sameBranch = async () => {
    if (lock && fs.existsSync(lock)) {
      for (let i = 0; i < 100 && fs.existsSync(lock); i++) await sleep(50);
      await sleep(200); // HEAD is written just after the lock is released
    }
    if (JSON.stringify(branchState(ctx.cwd)) === start) return true;
    stop(`The git branch changed. Stopped syncing to ${workspace} so it does not receive another branch's files.`);
    return false;
  };
  const syncer = createSyncer(ctx, workspace, sameBranch);
  const match = ignoreMatcher(ctx.cwd);
  const rel = (p: string) => path.relative(ctx.cwd, p).split(path.sep).join("/");
  let stopped = false;
  let timer: ReturnType<typeof setInterval> | undefined;
  const watcher = (opts.watch ?? chokidar.watch)(ctx.cwd, {
    ignoreInitial: true,
    ignored: (p, stats) => {
      const r = rel(p);
      return r !== "" && match(r, stats?.isDirectory() ?? false);
    },
    awaitWriteFinish: { stabilityThreshold: 100, pollInterval: 25 },
  });
  const close = async () => {
    stopped = true;
    syncer.stop();
    clearInterval(timer);
    await watcher.close();
  };
  const stop = (message: string, hint?: string) => {
    if (stopped) return;
    ctx.out(paint("red", `✖ ${message}`));
    if (hint) ctx.out(paint("yellow", hint));
    void close().then(() => opts.onStop?.());
  };
  watcher
    .on("add", (p) => void syncer.put(rel(p)))
    .on("change", (p) => void syncer.put(rel(p)))
    .on("unlink", (p) => void syncer.rm(rel(p)))
    .on("unlinkDir", (p) => void syncer.rm(rel(p)))
    .on("error", (err) => {
      const e = err as NodeJS.ErrnoException;
      if (e.code === "ENOSPC") {
        stop(`The file watcher failed: ${e.message}. Stopped syncing.`,
          "Linux limits how many files can be watched. Raise it with: sudo sysctl fs.inotify.max_user_watches=524288");
      } else if (e.code === "EMFILE") {
        stop(`The file watcher failed: ${e.message}. Stopped syncing.`, "Too many open files. Raise the limit with `ulimit -n 10240` and run again.");
      } else {
        ctx.out(paint("yellow", `! File watcher: ${e.message}`)); // e.g. one unreadable folder; the rest keeps syncing
      }
    });
  await new Promise<void>((resolve) => watcher.once("ready", () => resolve()));

  // Without file changes nothing reaches the guard above, so the branch is
  // also polled (git replaces .git/HEAD with a rename, which watchers miss).
  timer = setInterval(() => {
    if (stopped || JSON.stringify(branchState(ctx.cwd)) === start) return;
    stop(`The git branch changed. Stopped syncing to ${workspace} so it does not receive another branch's files.`);
  }, 1000);
  ctx.out("Watching for changes (Ctrl+C to stop)...");
  return { close, idle: () => syncer.idle() };
}

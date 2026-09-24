import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import path from "node:path";
import { ConfigError, NAME_RE } from "./config.js";

function git(cwd: string, args: string[]): string | null {
  try {
    return execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim();
  } catch {
    return null;
  }
}

export type BranchState = { kind: "branch"; name: string } | { kind: "detached" } | { kind: "none" };

export function branchState(cwd: string): BranchState {
  if (git(cwd, ["rev-parse", "--is-inside-work-tree"]) !== "true") return { kind: "none" };
  const name = git(cwd, ["symbolic-ref", "--short", "HEAD"]);
  return name ? { kind: "branch", name } : { kind: "detached" };
}

// gitPath resolves a path inside the git directory (worktree-aware), such as
// "index.lock", or returns null outside git.
export function gitPath(cwd: string, name: string): string | null {
  const p = git(cwd, ["rev-parse", "--git-path", name]);
  return p ? path.resolve(cwd, p) : null;
}

export function currentSha(cwd: string): string {
  return git(cwd, ["rev-parse", "HEAD"]) ?? "";
}

// workspaceFromBranch turns a branch name into a valid workspace name.
// main/master deploy to production and develop/dev to staging. Names longer
// than 40 characters are shortened with a hash so different branches never
// share a workspace.
export function workspaceFromBranch(branch: string): string {
  const name = branch.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  if (name === "main" || name === "master") return "production";
  if (name === "develop" || name === "dev") return "staging";
  if (name.length <= 40) return name;
  const hash = createHash("sha1").update(name).digest("hex").slice(0, 7);
  return `${name.slice(0, 32).replace(/-+$/, "")}-${hash}`;
}

// resolveWorkspace picks the workspace from -w, or from the current branch.
export function resolveWorkspace(cwd: string, explicit?: string): string {
  if (explicit !== undefined) {
    if (!NAME_RE.test(explicit)) throw new ConfigError(`Invalid workspace "${explicit}": use 1-40 characters of a-z, 0-9 and '-'.`);
    return explicit;
  }
  const state = branchState(cwd);
  if (state.kind === "none") return "production";
  if (state.kind === "detached") throw new ConfigError("HEAD is detached, so there is no branch to name the workspace after. Pass -w <workspace>.");
  const ws = workspaceFromBranch(state.name);
  if (!NAME_RE.test(ws)) throw new ConfigError(`Cannot derive a workspace name from branch "${state.name}". Pass -w <workspace>.`);
  return ws;
}

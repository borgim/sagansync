import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, expect, test } from "vitest";
import { NAME_RE } from "../src/lib/config.js";
import { branchState, resolveWorkspace, workspaceFromBranch } from "../src/lib/git.js";

describe("workspaceFromBranch", () => {
  test.each([
    ["main", "production"],
    ["master", "production"],
    ["develop", "staging"],
    ["dev", "staging"],
    ["feature/Login-Page", "feature-login-page"],
    ["fix__weird..name//", "fix-weird-name"],
    ["release/2026.09", "release-2026-09"],
  ])("%s -> %s", (branch, ws) => {
    expect(workspaceFromBranch(branch)).toBe(ws);
  });

  test("long names stay valid and distinct", () => {
    const a = workspaceFromBranch("feature/" + "x".repeat(60) + "-a");
    const b = workspaceFromBranch("feature/" + "x".repeat(60) + "-b");
    expect(a).toMatch(NAME_RE);
    expect(b).toMatch(NAME_RE);
    expect(a).not.toBe(b);
    expect(a.length).toBeLessThanOrEqual(40);
  });
});

function repo(branch: string): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-git-"));
  const run = (...args: string[]) => execFileSync("git", args, { cwd: dir, stdio: "ignore" });
  run("init", "-q", "-b", branch);
  run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init");
  return dir;
}

describe("resolveWorkspace", () => {
  test("uses the branch", () => {
    expect(resolveWorkspace(repo("feature/x"))).toBe("feature-x");
    expect(resolveWorkspace(repo("main"))).toBe("production");
  });

  test("outside git defaults to production", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-nogit-"));
    expect(branchState(dir)).toEqual({ kind: "none" });
    expect(resolveWorkspace(dir)).toBe("production");
  });

  test("detached HEAD requires -w", () => {
    const dir = repo("main");
    execFileSync("git", ["checkout", "-q", "--detach"], { cwd: dir });
    expect(() => resolveWorkspace(dir)).toThrow("-w");
    expect(resolveWorkspace(dir, "hotfix")).toBe("hotfix");
  });

  test("branch with no usable characters requires -w", () => {
    expect(() => resolveWorkspace(repo("日本"))).toThrow("-w");
  });

  test("explicit workspace is validated", () => {
    expect(() => resolveWorkspace(os.tmpdir(), "Bad Name")).toThrow("Invalid workspace");
  });

});

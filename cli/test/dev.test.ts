import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { afterEach, describe, expect, test, vi } from "vitest";
import { sshRemote, type Target } from "../src/lib/ssh.js";
import { fakeSsh } from "./helpers/fakeSsh.js";
import { createSyncer, dev, type DevSession } from "../src/commands/dev.js";
import { projectDir, testCtx } from "./helpers/ctx.js";
import { ev, FakeRemote, VERSION_OK } from "./helpers/fakeRemote.js";

const devDone = { stdout: ev({ type: "done", url: "https://feat-x.app.test" }) };

function gitRepo(branch: string): string {
  const dir = projectDir();
  const run = (...a: string[]) => execFileSync("git", a, { cwd: dir, stdio: "ignore" });
  run("init", "-q", "-b", branch);
  run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init");
  return dir;
}

describe("createSyncer", () => {
  test("sends puts and removes in order and reports failures without stopping", async () => {
    const remote = new FakeRemote({ put: (c) => ({ code: c.args[3] === "bad.ts" ? 1 : 0, stderr: "nope" }) });
    const ctx = testCtx(remote, projectDir({ "a.ts": "A", "bad.ts": "B" }));
    const s = createSyncer(ctx, "feat-x");
    void s.put("a.ts");
    void s.put("bad.ts");
    void s.rm("old.ts");
    await s.idle();
    expect(remote.calls.map((c) => c.args)).toEqual([
      ["put", "app", "feat-x", "a.ts"], ["put", "app", "feat-x", "bad.ts"], ["rm", "app", "feat-x", "old.ts"],
    ]);
    expect(remote.calls[0]!.stdin).toBe("A");
    expect(ctx.lines).toEqual(["  ↑ a.ts", expect.stringContaining("✖ ↑ bad.ts"), "  ✕ old.ts"]);
  });
});

test("createSyncer does not send a file it cannot read", async () => {
  // Real ssh plumbing: the bug only shows when the read fails mid-pipe.
  const ssh = fakeSsh({ code: 0 });
  const target: Target = { host: "vps.test", port: 22, user: "sagan", identityFile: "/k", knownHosts: "/kh", controlDir: os.tmpdir() };
  const ctx = testCtx(new FakeRemote(), projectDir({ "locked.ts": "secret" }));
  ctx.remote = sshRemote(target, ssh.bin);
  fs.chmodSync(path.join(ctx.cwd, "locked.ts"), 0o000);
  const s = createSyncer(ctx, "feat-x");
  void s.put("locked.ts");
  await s.idle();
  expect(ctx.lines).toEqual([expect.stringMatching(/✖ ↑ locked.ts: .*EACCES/)]);
});

describe("dev", () => {
  let session: DevSession | undefined;
  afterEach(async () => {
    await session?.close();
    session = undefined;
  });

  test("refuses production without --force", async () => {
    const remote = new FakeRemote({ version: VERSION_OK });
    await expect(dev(testCtx(remote), {})).rejects.toThrow("production");
    expect(remote.calls).toHaveLength(0);
  });

  test("starts dev mode with the command wrapped in sh -c", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, host: { stdout: '{"host":"feat-x.app.test"}' }, dev: devDone });
    const ctx = testCtx(remote);
    session = await dev(ctx, { workspace: "feat-x", command: "pnpm dev --port 3000", build: true });
    const args = remote.argsOf("dev")!;
    expect(args.slice(args.indexOf("--command"))).toEqual(["--command", '["sh","-c","pnpm dev --port 3000"]', "--build"]);
    expect(ctx.lines).toContain("✔ Dev server at https://feat-x.app.test");
  });

  test("syncs edits, skips ignored files", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, host: { stdout: "{}" }, dev: devDone });
    const ctx = testCtx(remote);
    session = await dev(ctx, { workspace: "feat-x" });
    fs.writeFileSync(path.join(ctx.cwd, ".env"), "SECRET=1");
    fs.writeFileSync(path.join(ctx.cwd, "new.ts"), "hello");
    await vi.waitFor(() => expect(remote.calls.some((c) => c.args[0] === "put")).toBe(true), { timeout: 5000 });
    await session.idle();
    const puts = remote.calls.filter((c) => c.args[0] === "put");
    expect(puts.map((c) => c.args[3])).toEqual(["new.ts"]);
    expect(puts[0]!.stdin).toBe("hello");
    fs.rmSync(path.join(ctx.cwd, "new.ts"));
    await vi.waitFor(() => expect(remote.calls.some((c) => c.args[0] === "rm" && c.args[3] === "new.ts")).toBe(true), { timeout: 5000 });
  });

  test("stops when the git branch changes", async () => {
    const cwd = gitRepo("feat-x");
    const remote = new FakeRemote({ version: VERSION_OK, host: { stdout: "{}" }, dev: devDone });
    const ctx = testCtx(remote, cwd);
    let stopped = false;
    session = await dev(ctx, { onBranchChange: () => (stopped = true) });
    execFileSync("git", ["checkout", "-q", "-b", "other"], { cwd });
    await vi.waitFor(() => expect(stopped).toBe(true), { timeout: 5000 });
    expect(ctx.lines.some((l) => l.includes("branch changed"))).toBe(true);
  });
});

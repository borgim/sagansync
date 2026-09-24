// Contract test: the real CLI talks to the real `sagand gateway` and API
// through a fake ssh, with a fake container runtime behind the API. It
// proves the quoting, flags, stdin payloads and event parsing agree on both
// sides. Needs Go; skipped when it is not installed.
import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterAll, beforeAll, describe, expect, test, vi } from "vitest";
import type { Ctx } from "../src/commands/context.js";
import { deploy } from "../src/commands/deploy.js";
import { dev, type DevSession } from "../src/commands/dev.js";
import { envList, envSet } from "../src/commands/env.js";
import { list } from "../src/commands/list.js";
import { logs } from "../src/commands/logs.js";
import { remove } from "../src/commands/remove.js";
import { validateConfig } from "../src/lib/config.js";
import { sshRemote, type Target } from "../src/lib/ssh.js";
import { pointsToVps, projectDir } from "./helpers/ctx.js";

const agentDir = path.resolve(import.meta.dirname, "../../agent");
const hasGo = (() => {
  try {
    execFileSync("go", ["version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
})();

describe.skipIf(!hasGo)("CLI <-> sagand contract", () => {
  let tmp: string;
  let data: string;
  let daemon: ChildProcess;
  let target: Target;
  let sshBin: string;

  beforeAll(async () => {
    tmp = fs.mkdtempSync("/tmp/sgc-"); // short: macOS limits socket paths to 104 bytes
    data = path.join(tmp, "data");
    const bin = path.join(tmp, "bin");
    const build = (pkg: string, out: string, ...flags: string[]) =>
      execFileSync("go", ["build", ...flags, "-o", path.join(bin, out), pkg], { cwd: agentDir, stdio: "inherit" });
    build("./cmd/sagand", "sagand", "-ldflags", "-X main.version=0.1.0");
    build("./internal/testutil/cmd/fakesagand", "fakesagand");
    const sock = path.join(tmp, "s.sock");
    daemon = spawn(path.join(bin, "fakesagand"), [sock, data], { stdio: ["ignore", "pipe", "inherit"] });
    await new Promise<void>((resolve) => daemon.stdout!.once("data", () => resolve()));

    // The fake ssh behaves like sshd with the forced command: whatever the
    // client asked for becomes SSH_ORIGINAL_COMMAND for `sagand gateway`.
    sshBin = path.join(tmp, "ssh");
    fs.writeFileSync(sshBin, `#!/bin/sh
for last; do :; done
SSH_ORIGINAL_COMMAND="$last" SAGAND_SOCKET="${sock}" exec "${bin}/sagand" gateway
`, { mode: 0o755 });
    target = { host: "vps.test", port: 22, user: "sagan", identityFile: "/dev/null", knownHosts: "/dev/null", controlDir: tmp };
  }, 180_000);

  afterAll(() => {
    daemon?.kill();
    fs.rmSync(tmp, { recursive: true, force: true });
  });

  function ctx(cwd = projectDir()): Ctx & { lines: string[] } {
    const lines: string[] = [];
    const config = validateConfig({ host: "vps.test", project: "app", internalPort: 3000, domain: "app.test", previewDomain: "preview.test" });
    return { cwd, config, remote: sshRemote(target, sshBin), lookup: pointsToVps, out: (s) => lines.push(s), lines };
  }

  test("deploy, list and logs", async () => {
    const c = ctx();
    await deploy(c, {});
    expect(c.lines.at(-1)).toBe("✔ Live at https://app.test");
    await list(c, {});
    expect(c.lines.find((l) => l.startsWith("production"))).toContain("running");
    await logs(c, { tail: 5 });
    expect(c.lines.at(-1)).toMatch(/^log from sagan_app_production_/);
  });

  test("env values with quotes, newlines and $ arrive intact", async () => {
    const c = ctx();
    const tricky = `it's "quoted" $HOME \`id\` a=b\nsecond line`;
    await envSet(c, [`TRICKY=${tricky}`, "PLAIN=1"], {});
    await envList(c, {});
    expect(c.lines.slice(-2)).toEqual(["PLAIN=********", "TRICKY=********"]);
    const stored = JSON.parse(fs.readFileSync(path.join(data, "env", "app", "production.json"), "utf8"));
    expect(stored.TRICKY).toBe(tricky);
  });

  test("dev mode starts on a preview host and syncs files with awkward names", async () => {
    const c = ctx();
    let session: DevSession | undefined;
    try {
      session = await dev(c, { workspace: "feat-x", command: "npm run dev" });
      expect(c.lines).toContain("✔ Dev server at https://feat-x-app.preview.test");
      fs.writeFileSync(path.join(c.cwd, "it's a file.ts"), "content");
      const synced = path.join(data, "srv", "app", "feat-x", "dev", "it's a file.ts");
      await vi.waitFor(() => expect(fs.readFileSync(synced, "utf8")).toBe("content"), { timeout: 10_000 });
    } finally {
      await session?.close();
    }
  });

  test("errors from the agent become clear messages", async () => {
    const c = ctx();
    await expect(logs(c, { workspace: "ghost" })).rejects.toThrow("does not exist");
    const denied = await sshRemote(target, sshBin).run(["daemon"]);
    expect(denied.code).toBe(126);
  });

  test("remove", async () => {
    const c = ctx();
    await remove(c, { workspace: "feat-x", yes: true, confirm: async () => true });
    expect(c.lines.at(-1)).toBe("✔ Removed app/feat-x");
  });
});

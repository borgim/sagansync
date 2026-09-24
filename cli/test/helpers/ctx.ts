import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import type { Ctx } from "../../src/commands/context.js";
import { validateConfig } from "../../src/lib/config.js";
import type { Lookup } from "../../src/lib/dns.js";
import { FakeRemote } from "./fakeRemote.js";

// projectDir creates a throwaway project (outside git, so the workspace
// defaults to "production").
export function projectDir(files: Record<string, string> = { "Dockerfile": "FROM node\n", "server.js": "1\n" }): string {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-proj-"));
  for (const [rel, body] of Object.entries(files)) {
    fs.mkdirSync(path.dirname(path.join(root, rel)), { recursive: true });
    fs.writeFileSync(path.join(root, rel), body);
  }
  return root;
}

export const pointsToVps: Lookup = async () => ["203.0.113.7"];

export function testCtx(remote: FakeRemote, cwd = projectDir(), extra: Record<string, unknown> = {}): Ctx & { lines: string[] } {
  const lines: string[] = [];
  const config = validateConfig({ host: "vps.test", project: "app", internalPort: 3000, domain: "app.test", ...extra });
  return { cwd, config, remote, lookup: pointsToVps, out: (s) => lines.push(s), lines };
}

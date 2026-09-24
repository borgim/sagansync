import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, expect, test } from "vitest";
import { defaultProject, init, type InitAnswers } from "../src/commands/init.js";
import { loadConfig } from "../src/lib/config.js";

const answers: InitAnswers = { host: "203.0.113.7", sshPort: 22, project: "app", internalPort: 3000, domain: "api.example.com", previewDomain: "example.com" };
let home: string;

beforeEach(() => {
  home = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-home-"));
  process.env.SAGANSYNC_HOME = home;
});
afterEach(() => {
  delete process.env.SAGANSYNC_HOME;
});

function deps(overrides: Partial<Parameters<typeof init>[1]> = {}) {
  const lines: string[] = [];
  const keys: string[] = [];
  return {
    lines, keys,
    d: { ask: async () => answers, confirmOverwrite: async () => false, keygen: (k: string) => { keys.push(k); fs.writeFileSync(k, "key"); }, out: (s: string) => lines.push(s), ...overrides },
  };
}

test("writes the config, creates the key and prints DNS records", async () => {
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-init-"));
  const { d, lines, keys } = deps();
  await init(cwd, d);
  expect(loadConfig(cwd)).toEqual({ ...answers, user: "sagan" });
  expect(keys).toEqual([path.join(home, "keys", "203.0.113.7_ed25519")]);
  expect(lines).toContain("  A  *.example.com  ->  203.0.113.7   (one wildcard for every project's branches)");
  expect(lines).toContain("  sagansync provision --admin root@203.0.113.7   # installs sagand on the VPS");
});

test("reuses an existing key", async () => {
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-init-"));
  fs.mkdirSync(path.join(home, "keys"), { recursive: true });
  fs.writeFileSync(path.join(home, "keys", "203.0.113.7_ed25519"), "old");
  const { d, keys } = deps();
  await init(cwd, d);
  expect(keys).toEqual([]);
});

test("keeps an existing config unless confirmed", async () => {
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-init-"));
  fs.mkdirSync(path.join(cwd, ".sagansync"));
  fs.writeFileSync(path.join(cwd, ".sagansync", "config.json"), "{}");
  const { d } = deps();
  expect(await init(cwd, d)).toBeNull();
  expect(fs.readFileSync(path.join(cwd, ".sagansync", "config.json"), "utf8")).toBe("{}");
});

test("rejects invalid answers", async () => {
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-init-"));
  const { d } = deps({ ask: async () => ({ ...answers, project: "Bad Name" }) });
  await expect(init(cwd, d)).rejects.toThrow("project");
});

test("defaultProject sanitizes the folder name", () => {
  expect(defaultProject("/work/My Cool_App")).toBe("my-cool-app");
  expect(defaultProject("/work/日本")).toBe("app");
});

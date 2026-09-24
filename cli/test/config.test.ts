import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, expect, test } from "vitest";
import { ConfigError, keyPath, loadConfig, saveConfig, validateConfig } from "../src/lib/config.js";

const base = { host: "vps.example.com", project: "app", internalPort: 3000 };

describe("validateConfig", () => {
  test("fills defaults", () => {
    expect(validateConfig(base)).toEqual({ ...base, sshPort: 22, user: "sagan" });
  });

  test("keeps optional fields", () => {
    const full = { ...base, sshPort: 2222, user: "deploy", domain: "api.example.com", previewDomain: "example.com", healthPath: "/health", healthTimeout: 90, identityFile: "/k" };
    expect(validateConfig(full)).toEqual(full);
  });

  test.each([
    [{ ...base, host: "root@1.2.3.4" }, "host"],
    [{ ...base, host: "-oProxyCommand=evil" }, "host"],
    [{ ...base, project: "My App" }, "project"],
    [{ ...base, project: "a".repeat(41) }, "project"],
    [{ ...base, internalPort: 0 }, "internalPort"],
    [{ ...base, domain: "https://example.com" }, "domain"],
    [{ ...base, previewDomain: "*.example.com" }, "previewDomain"],
    [{ ...base, healthPath: "health" }, "healthPath"],
    [{ ...base, healthTimeout: 0 }, "healthTimeout"],
    [{ ...base, user: "Root" }, "user"],
  ])("rejects %j", (raw, field) => {
    expect(() => validateConfig(raw)).toThrow(ConfigError);
    expect(() => validateConfig(raw)).toThrow(field);
  });

  test("accepts IPv4 and IPv6 hosts", () => {
    expect(validateConfig({ ...base, host: "203.0.113.7" }).host).toBe("203.0.113.7");
    expect(validateConfig({ ...base, host: "2001:db8::1" }).host).toBe("2001:db8::1");
  });
});

describe("loadConfig / saveConfig", () => {
  test("round trip", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-"));
    saveConfig(dir, validateConfig(base));
    expect(loadConfig(dir).project).toBe("app");
  });

  test("missing config explains how to create it", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-"));
    expect(() => loadConfig(dir)).toThrow("sagansync init");
  });

  test("broken JSON is a ConfigError", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-"));
    fs.mkdirSync(path.join(dir, ".sagansync"));
    fs.writeFileSync(path.join(dir, ".sagansync", "config.json"), "{nope");
    expect(() => loadConfig(dir)).toThrow(ConfigError);
  });
});

test("keyPath defaults under SAGANSYNC_HOME and honors identityFile", () => {
  process.env.SAGANSYNC_HOME = "/tmp/sgs-home";
  expect(keyPath({ host: "vps.example.com" })).toBe("/tmp/sgs-home/keys/vps.example.com_ed25519");
  expect(keyPath({ host: "vps.example.com", identityFile: "/k" })).toBe("/k");
  delete process.env.SAGANSYNC_HOME;
});

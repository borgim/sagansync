import { createHash } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { gzipSync } from "node:zlib";
import { extract as tarExtract } from "tar-stream";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { adminTarget, archFor, bundle, checkBinary, daemonSettings, downloadAgent, provision, remoteScript } from "../src/commands/provision.js";
import type { Input, RunResult, Shell } from "../src/lib/ssh.js";
import { testCtx } from "./helpers/ctx.js";
import { FakeRemote, VERSION_OK } from "./helpers/fakeRemote.js";

async function untar(buf: Buffer): Promise<Record<string, { data: Buffer; mode: number }>> {
  const ex = tarExtract();
  const out: Record<string, { data: Buffer; mode: number }> = {};
  const done = new Promise<void>((resolve, reject) => {
    ex.on("entry", (h, body, next) => {
      const chunks: Buffer[] = [];
      body.on("data", (c) => chunks.push(c as Buffer));
      body.on("end", () => {
        out[h.name] = { data: Buffer.concat(chunks), mode: h.mode ?? 0 };
        next();
      });
    });
    ex.on("finish", resolve);
    ex.on("error", reject);
  });
  ex.end(buf);
  await done;
  return out;
}

// elf returns a minimal ELF header for the given machine (0x3e amd64, 0xb7 arm64).
function elf(machine: number): Buffer {
  const b = Buffer.alloc(64);
  b.writeUInt32BE(0x7f454c46, 0);
  b.writeUInt16LE(machine, 18);
  return b;
}

class FakeShell implements Shell {
  calls: { command: string; stdin?: Buffer }[] = [];
  constructor(private readonly reply: (command: string) => Partial<RunResult> & { lines?: string[] }) {}
  async run(command: string, stdin?: Input) {
    this.calls.push({ command, stdin: stdin as Buffer | undefined });
    const r = this.reply(command);
    return { code: r.code ?? 0, stdout: r.stdout ?? "", stderr: r.stderr ?? "" };
  }
  async stream(command: string, onLine: (l: string) => void, stdin?: Input) {
    const r = await this.run(command, stdin);
    for (const l of this.reply(command).lines ?? []) onLine(l);
    return { code: r.code, stderr: r.stderr };
  }
}

let home: string;
beforeEach(() => {
  home = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-home-"));
  process.env.SAGANSYNC_HOME = home;
  fs.mkdirSync(path.join(home, "keys"));
  fs.writeFileSync(path.join(home, "keys", "vps.test_ed25519.pub"), "ssh-ed25519 AAAAC3Nza deploy\n");
});
afterEach(() => {
  delete process.env.SAGANSYNC_HOME;
});

describe("adminTarget", () => {
  const cfg = testCtx(new FakeRemote()).config;
  test("defaults to root on the configured host", () => {
    expect(adminTarget(cfg, {})).toMatchObject({ user: "root", host: "vps.test", port: 22 });
  });
  test("accepts user@host and a key", () => {
    expect(adminTarget(cfg, { admin: "ubuntu@203.0.113.7", adminKey: "/k" })).toMatchObject({ user: "ubuntu", host: "203.0.113.7", identityFile: "/k" });
  });
  test("rejects garbage", () => {
    expect(() => adminTarget(cfg, { admin: "root@-oProxyCommand=x" })).toThrow("Invalid --admin");
  });
});

test("archFor and checkBinary", () => {
  expect(archFor("x86_64\n").name).toBe("amd64");
  expect(archFor("aarch64").name).toBe("arm64");
  expect(() => archFor("riscv64")).toThrow("not supported");
  expect(() => checkBinary(elf(0xb7), archFor("aarch64"))).not.toThrow();
  expect(() => checkBinary(elf(0x3e), archFor("aarch64"))).toThrow("GOARCH=arm64");
  expect(() => checkBinary(Buffer.from("#!/bin/sh\necho hi\n"), archFor("x86_64"))).toThrow("GOOS=linux");
});

describe("downloadAgent", () => {
  async function release(binary: Buffer, tamper = false) {
    const tgz = gzipSync(await bundle([{ name: "sagand", data: binary, mode: 0o755 }]));
    const sum = createHash("sha256").update(tamper ? Buffer.from("other") : tgz).digest("hex");
    const files: Record<string, Buffer> = {
      "v0.1.0/checksums.txt": Buffer.from(`${sum}  sagand_0.1.0_linux_arm64.tar.gz\nabc  sagand_0.1.0_linux_amd64.tar.gz\n`),
      "v0.1.0/sagand_0.1.0_linux_arm64.tar.gz": tgz,
    };
    const urls: string[] = [];
    const fetchFn = (async (url: string) => {
      urls.push(url);
      const f = files[url.split("/").slice(-2).join("/")];
      return f ? new Response(new Uint8Array(f)) : new Response("nope", { status: 404 });
    }) as typeof fetch;
    return { fetchFn, urls };
  }

  test("downloads, verifies and extracts the binary", async () => {
    const { fetchFn, urls } = await release(elf(0xb7));
    expect(await downloadAgent("arm64", fetchFn, "0.1.0")).toEqual(elf(0xb7));
    expect(urls[0]).toBe("https://github.com/borgim/sagansync/releases/download/v0.1.0/checksums.txt");
  });

  test("refuses a tampered archive", async () => {
    const { fetchFn } = await release(elf(0xb7), true);
    await expect(downloadAgent("arm64", fetchFn, "0.1.0")).rejects.toThrow("Checksum mismatch");
  });

  test("explains a missing release", async () => {
    const { fetchFn } = await release(elf(0xb7));
    await expect(downloadAgent("arm64", fetchFn, "9.9.9")).rejects.toThrow("HTTP 404");
  });
});

test("remoteScript runs provision.sh as root or through passwordless sudo, under sh", () => {
  const s = remoteScript(["--upgrade"]);
  expect(s.startsWith("sh -c '")).toBe(true);
  expect(s).toContain(`sudo -n bash "$d/provision.sh" "$d" '"'"'--upgrade'"'"'`);
});

test("daemonSettings", () => {
  expect(JSON.parse(daemonSettings({}))).toEqual({});
  expect(JSON.parse(daemonSettings({ acmeEmail: "ops@example.com", acmeCa: "https://localhost:14000/dir", acmeRootCa: "/x.pem" }))).toEqual({
    acmeEmail: "ops@example.com", acmeCA: "https://localhost:14000/dir", acmeRootCA: "/var/lib/sagand/acme-root-ca.pem",
  });
});

describe("provision", () => {
  test("uploads the bundle, streams progress and checks the deploy key", async () => {
    const bin = path.join(home, "sagand");
    fs.writeFileSync(bin, elf(0xb7));
    const shell = new FakeShell((cmd) => (cmd === "uname -m" ? { stdout: "aarch64\n" } : { lines: ["▸ Installing Podman", "✔ sagand is running"] }));
    const remote = new FakeRemote({ version: VERSION_OK });
    const ctx = testCtx(remote);
    await provision(ctx, { agentBinary: bin, upgrade: true, acmeEmail: "ops@example.com" }, { shell });
    const files = await untar(shell.calls[1]!.stdin!);
    expect(Object.keys(files).sort()).toEqual(["config.json", "deploy.pub", "provision.sh", "sagand"]);
    expect(files["sagand"]!.mode).toBe(0o755);
    expect(files["deploy.pub"]!.data.toString()).toContain("ssh-ed25519");
    expect(files["provision.sh"]!.data.toString()).toContain("sagand gateway");
    expect(JSON.parse(files["config.json"]!.data.toString())).toEqual({ acmeEmail: "ops@example.com" });
    expect(shell.calls[1]!.command).toContain("--upgrade");
    expect(ctx.lines).toContain("▸ Installing Podman");
    expect(remote.argsOf("version")).toEqual(["version"]);
    expect(ctx.lines.at(-1)).toContain("is ready");
  });

  test("needs the deploy key", async () => {
    fs.rmSync(path.join(home, "keys", "vps.test_ed25519.pub"));
    await expect(provision(testCtx(new FakeRemote()), {}, { shell: new FakeShell(() => ({})) })).rejects.toMatchObject({ hint: expect.stringContaining("sagansync init") });
  });

  test("explains sudo that needs a password", async () => {
    const bin = path.join(home, "sagand");
    fs.writeFileSync(bin, elf(0xb7));
    const shell = new FakeShell((cmd) => (cmd === "uname -m" ? { stdout: "aarch64" } : { code: 1, stderr: "sudo: a password is required\n" }));
    await expect(provision(testCtx(new FakeRemote()), { agentBinary: bin }, { shell })).rejects.toMatchObject({ hint: expect.stringContaining("--admin root@") });
  });

  test.each([
    ["sudo-rs (Ubuntu 25.10+)", "sudo: interactive authentication is required\n"],
    ["no sudo installed", "sh: 1: sudo: not found\n"],
  ])("explains %s", async (_name, stderr) => {
    const bin = path.join(home, "sagand");
    fs.writeFileSync(bin, elf(0xb7));
    const shell = new FakeShell((cmd) => (cmd === "uname -m" ? { stdout: "aarch64" } : { code: 1, stderr }));
    await expect(provision(testCtx(new FakeRemote()), { agentBinary: bin }, { shell })).rejects.toMatchObject({ hint: expect.stringContaining("--admin root@") });
  });

  test("shows provision.sh's own error message", async () => {
    const bin = path.join(home, "sagand");
    fs.writeFileSync(bin, elf(0xb7));
    const shell = new FakeShell((cmd) => (cmd === "uname -m" ? { stdout: "aarch64" } : { code: 1, stderr: "✖ Ubuntu 22.04 is too old: sagand needs Ubuntu 24.04 or newer (for Podman 4.3+).\n" }));
    await expect(provision(testCtx(new FakeRemote()), { agentBinary: bin }, { shell })).rejects.toThrow("Ubuntu 22.04 is too old");
  });

  test("an SSH failure is explained", async () => {
    const shell = new FakeShell(() => ({ code: 255, stderr: "Permission denied (publickey)." }));
    await expect(provision(testCtx(new FakeRemote()), {}, { shell })).rejects.toThrow("Could not connect over SSH");
  });
});

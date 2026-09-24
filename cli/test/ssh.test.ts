import os from "node:os";
import { Readable } from "node:stream";
import { describe, expect, test } from "vitest";
import { remoteCommand, shQuote, sshArgs, sshRemote, type Target } from "../src/lib/ssh.js";
import { fakeSsh } from "./helpers/fakeSsh.js";

const target: Target = { host: "vps.example.com", port: 2222, user: "sagan", identityFile: "/keys/id",
  knownHosts: "/cfg/known_hosts", controlDir: os.tmpdir() };

describe("shQuote", () => {
  test.each([
    ["plain", "'plain'"],
    ["it's", `'it'"'"'s'`],
    ["$(rm -rf /); `id`", "'$(rm -rf /); `id`'"],
    ["", "''"],
  ])("%s", (input, quoted) => {
    expect(shQuote(input)).toBe(quoted);
  });

  test("remoteCommand quotes every argument", () => {
    expect(remoteCommand(["deploy", "--project", "a b"])).toBe("'sagand' 'deploy' '--project' 'a b'");
  });
});

describe("sshArgs", () => {
  const args = sshArgs(target);
  test("verifies host keys and never disables checking", () => {
    expect(args.join(" ")).toContain("StrictHostKeyChecking=accept-new");
    expect(args.join(" ")).toContain("UserKnownHostsFile=/cfg/known_hosts");
    expect(args.join(" ")).not.toContain("StrictHostKeyChecking=no");
  });
  test("reuses connections", () => {
    expect(args).toContain("ControlMaster=auto");
    expect(args).toContain("ControlPersist=60s");
    expect(args.some((a) => a.startsWith("ControlPath=") && a.endsWith("cm-%C"))).toBe(true);
  });
  test("uses the deploy key only and never prompts", () => {
    expect(args).toEqual(expect.arrayContaining(["-i", "/keys/id", "IdentitiesOnly=yes", "BatchMode=yes"]));
  });
  test("ends with -- and the host so the host can never be read as an option", () => {
    expect(args.slice(-2)).toEqual(["--", "vps.example.com"]);
    expect(args.slice(args.indexOf("-p"), args.indexOf("-p") + 2)).toEqual(["-p", "2222"]);
  });
});

describe("sshRemote", () => {
  test("run passes the quoted command and stdin, and returns output", async () => {
    const ssh = fakeSsh({ stdout: '{"version":"0.1.0"}\n', code: 0 });
    const r = await sshRemote(target, ssh.bin).run(["env", "set", "--project", "app"], '{"A":"x"}');
    expect(r).toEqual({ code: 0, stdout: '{"version":"0.1.0"}\n', stderr: "" });
    const rec = ssh.record();
    expect(rec.argv.at(-1)).toBe("'sagand' 'env' 'set' '--project' 'app'");
    expect(rec.stdin).toBe('{"A":"x"}');
  });

  test("stream delivers lines and pipes a stream to stdin", async () => {
    const ssh = fakeSsh({ stdout: "one\ntwo\n", code: 3, stderr: "boom" });
    const lines: string[] = [];
    const r = await sshRemote(target, ssh.bin).stream(["deploy"], (l) => lines.push(l), Readable.from(["tar", "ball"]));
    expect(lines).toEqual(["one", "two"]);
    expect(r).toEqual({ code: 3, stderr: "boom" });
    expect(ssh.record().stdin).toBe("tarball");
  });

  test("a failing input stream rejects instead of sending a truncated body", async () => {
    const ssh = fakeSsh({ code: 0 });
    const broken = new Readable({ read() { this.destroy(new Error("EACCES: permission denied")); } });
    await expect(sshRemote(target, ssh.bin).run(["put", "app", "ws", "a.ts"], broken)).rejects.toThrow("EACCES");
  });

  test("a missing ssh binary rejects", async () => {
    await expect(sshRemote(target, "/nonexistent/ssh").run(["version"])).rejects.toThrow();
  });
});

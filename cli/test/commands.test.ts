import fs from "node:fs";
import path from "node:path";
import { gunzipSync } from "node:zlib";
import { describe, expect, test } from "vitest";
import { deploy } from "../src/commands/deploy.js";
import { envList, envSet, envUnset, parseDotenv, parsePairs } from "../src/commands/env.js";
import { list } from "../src/commands/list.js";
import { logs } from "../src/commands/logs.js";
import { remove } from "../src/commands/remove.js";
import { projectDir, testCtx } from "./helpers/ctx.js";
import { ev, FakeRemote, VERSION_OK } from "./helpers/fakeRemote.js";

const doneDeploy = { stdout: [ev({ type: "step", name: "build" }), ev({ type: "done", url: "https://app.test", release: "r1", hostPort: 41000 })].join("\n") };

describe("deploy", () => {
  test("uploads the project and reports the URL", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, host: { stdout: '{"host":"app.test"}' }, deploy: doneDeploy });
    const ctx = testCtx(remote, projectDir({ "Dockerfile": "FROM node\n", ".env": "SECRET=1" }));
    await deploy(ctx, {});
    expect(remote.argsOf("deploy")).toEqual(["deploy", "--project", "app", "--workspace", "production", "--port", "3000", "--domain", "app.test"]);
    const upload = gunzipSync(Buffer.from(remote.calls.find((c) => c.args[0] === "deploy")!.stdin, "latin1")).toString("latin1");
    expect(upload).toContain("Dockerfile");
    expect(upload).not.toContain("SECRET=1");
    expect(ctx.lines.at(-1)).toBe("✔ Live at https://app.test");
  });

  test("warns about DNS without failing", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, host: { stdout: '{"host":"app.test"}' }, deploy: doneDeploy });
    const ctx = testCtx(remote);
    ctx.lookup = async (h) => (h === "vps.test" ? ["203.0.113.7"] : ["198.51.100.1"]);
    await deploy(ctx, {});
    expect(ctx.lines.some((l) => l.includes("198.51.100.1"))).toBe(true);
    expect(ctx.lines.at(-1)).toContain("Live at");
  });

  test("skips the DNS check without domains and shows the port", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, deploy: { stdout: ev({ type: "done", hostPort: 41000 }) } });
    const ctx = testCtx(remote);
    delete ctx.config.domain;
    await deploy(ctx, {});
    expect(remote.argsOf("host")).toBeUndefined();
    expect(ctx.lines.at(-1)).toContain("port 41000");
  });

  test("an explicit workspace is used", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, deploy: doneDeploy });
    await deploy(testCtx(remote), { workspace: "feat-x" });
    expect(remote.argsOf("deploy")).toContain("feat-x");
  });
});

describe("list", () => {
  const statuses = JSON.stringify([
    { project: "app", workspace: "production", mode: "deploy", release: "r1", url: "https://app.test", hostPort: 1, running: true },
    { project: "other", workspace: "production", mode: "deploy", release: "r9", hostPort: 2, running: false },
  ]);
  test("shows this project's workspaces", async () => {
    const ctx = testCtx(new FakeRemote({ version: VERSION_OK, list: { stdout: statuses } }));
    await list(ctx, {});
    expect(ctx.lines).toEqual(["WORKSPACE   MODE    STATUS   RELEASE  URL", "production  deploy  running  r1       https://app.test"]);
  });
  test("--all shows every project", async () => {
    const ctx = testCtx(new FakeRemote({ version: VERSION_OK, list: { stdout: statuses } }));
    await list(ctx, { all: true });
    expect(ctx.lines).toHaveLength(3);
    expect(ctx.lines[2]).toContain("127.0.0.1:2");
  });
});

describe("logs", () => {
  test("prints lines and passes flags", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, logs: { stdout: "hello\n{\"level\":\"info\"}\n" } });
    const ctx = testCtx(remote);
    await logs(ctx, { tail: 20, follow: true });
    expect(remote.argsOf("logs")).toEqual(["logs", "--project", "app", "--workspace", "production", "--tail", "20", "-f"]);
    expect(ctx.lines).toEqual(["hello", '{"level":"info"}']);
  });
  test("turns an error event into an error", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, logs: { code: 1, stdout: ev({ type: "error", code: "not_found", message: "workspace app/production does not exist" }) } });
    await expect(logs(testCtx(remote), {})).rejects.toThrow("does not exist");
  });
});

describe("remove", () => {
  test("asks first and can be cancelled", async () => {
    const remote = new FakeRemote({ version: VERSION_OK });
    const ctx = testCtx(remote);
    await remove(ctx, { confirm: async () => false });
    expect(remote.calls).toHaveLength(0);
    expect(ctx.lines).toEqual(["Cancelled."]);
  });
  test("--yes removes without asking", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, remove: { stdout: ev({ type: "done" }) } });
    const ctx = testCtx(remote);
    await remove(ctx, { yes: true, confirm: async () => { throw new Error("should not ask"); } });
    expect(remote.argsOf("remove")).toEqual(["remove", "--project", "app", "--workspace", "production"]);
    expect(ctx.lines.at(-1)).toBe("✔ Removed app/production");
  });
});

describe("env", () => {
  test("parseDotenv", () => {
    expect(parseDotenv('# c\n\nexport A=1\nB = "two words"\nC=\'x=y\'\nD="l1\\nl2"\nE=\n')).toEqual({ A: "1", B: "two words", C: "x=y", D: "l1\nl2", E: "" });
    expect(() => parseDotenv("not a pair")).toThrow("Line 1");
  });

  test("parseDotenv handles inline comments and quotes like dotenv", () => {
    const text = [
      "A=1 # comment",
      'B="x y" # c',
      "C='a # not a comment'",
      "D=url#fragment",
      'E="say \\"hi\\""',
      "F=  spaced value  ",
    ].join("\n");
    expect(parseDotenv(text)).toEqual({ A: "1", B: "x y", C: "a # not a comment", D: "url#fragment", E: 'say "hi"', F: "spaced value" });
    expect(() => parseDotenv('G="never closed')).toThrow("Line 1");
    expect(() => parseDotenv('H="x" trailing')).toThrow("Line 1");
  });

  test("parsePairs keeps everything after the first =", () => {
    expect(parsePairs(["URL=postgres://u:p@h/db?a=b"])).toEqual({ URL: "postgres://u:p@h/db?a=b" });
    expect(() => parsePairs(["=x"])).toThrow("KEY=VALUE");
    expect(() => parsePairs(["1A=x"])).toThrow("KEY=VALUE");
  });

  test("set sends values on stdin, merging a file and pairs", async () => {
    const remote = new FakeRemote({ version: VERSION_OK });
    const ctx = testCtx(remote);
    const file = path.join(ctx.cwd, ".env.production");
    fs.writeFileSync(file, "A=from-file\nB=file\n");
    await envSet(ctx, ["B=pair", "C=it's \"quoted\""], { file });
    const call = remote.calls.find((c) => c.args[0] === "env")!;
    expect(call.args).toEqual(["env", "set", "--project", "app", "--workspace", "production"]);
    expect(JSON.parse(call.stdin)).toEqual({ A: "from-file", B: "pair", C: `it's "quoted"` });
    expect(call.args.join(" ")).not.toContain("from-file");
  });

  test("set needs something to set", async () => {
    await expect(envSet(testCtx(new FakeRemote()), [], {})).rejects.toMatchObject({ exitCode: 2 });
  });

  test("unset and list", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, "env list": { stdout: '{"keys":["A","B"]}' } });
    const ctx = testCtx(remote);
    await envUnset(ctx, ["A"], {});
    await envList(ctx, {});
    expect(remote.calls.find((c) => c.args[1] === "unset")!.args.slice(-1)).toEqual(["A"]);
    expect(ctx.lines.slice(-2)).toEqual(["A=********", "B=********"]);
  });
});

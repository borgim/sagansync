import { describe, expect, test } from "vitest";
import { checkAgent, CliError, failure, requestFlags, runStreaming, sshError } from "../src/lib/agent.js";
import { validateConfig } from "../src/lib/config.js";
import { ev, FakeRemote, VERSION_OK } from "./helpers/fakeRemote.js";

const cfg = validateConfig({ host: "vps.test", project: "app", internalPort: 3000, domain: "app.test", previewDomain: "preview.test", healthPath: "/health", healthTimeout: 90 });

describe("checkAgent", () => {
  test("passes on a matching agent", async () => {
    expect(await checkAgent(new FakeRemote({ version: VERSION_OK }))).toBeUndefined();
  });
  test("warns on a different compatible version", async () => {
    const w = await checkAgent(new FakeRemote({ version: { stdout: '{"version":"0.2.0","protocol":1}' } }));
    expect(w).toContain("0.2.0");
  });
  test("refuses another protocol", async () => {
    await expect(checkAgent(new FakeRemote({ version: { stdout: '{"version":"1.0.0","protocol":2}' } }))).rejects.toThrow("protocol 2");
  });
  test("explains an SSH failure", async () => {
    const remote = new FakeRemote({ version: { code: 255, stderr: "Permission denied (publickey)." } });
    await expect(checkAgent(remote)).rejects.toMatchObject({ hint: expect.stringContaining("provision") });
  });
});

describe("failure", () => {
  test("uses the error event on stdout", () => {
    const e = failure({ code: 1, stdout: ev({ type: "error", code: "daemon_unavailable", message: "cannot reach" }) + "\n", stderr: "" });
    expect(e.message).toBe("cannot reach");
    expect(e.hint).toContain("provision");
  });
  test("explains a denied command", () => {
    expect(failure({ code: 126, stdout: "", stderr: 'sagand: command not allowed: "foo"' }).message).toContain("refused");
  });
  test("host key change gets a specific hint", () => {
    expect(sshError("@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@").hint).toContain("known_hosts");
  });
});

describe("runStreaming", () => {
  test("returns the done event and renders progress", async () => {
    const out: string[] = [];
    const remote = new FakeRemote({ deploy: { stdout: [ev({ type: "step", name: "build" }), ev({ type: "done", url: "https://app.test" })].join("\n") } });
    const done = await runStreaming(remote, ["deploy"], { out: (s) => out.push(s) });
    expect(done.url).toBe("https://app.test");
    expect(out).toEqual(["▸ Building image"]);
  });
  test("throws the error event with logs and exit code", async () => {
    const remote = new FakeRemote({ deploy: { code: 1, stdout: ev({ type: "error", code: "health_failed", message: "exited", logs: ["boom"] }) } });
    const err = (await runStreaming(remote, ["deploy"], { out: () => {} }).catch((e) => e)) as CliError;
    expect(err).toBeInstanceOf(CliError);
    expect(err.exitCode).toBe(1);
    expect(err.details).toEqual(["Last container logs:", "  boom"]);
  });
  test("validation errors exit with 2", async () => {
    const remote = new FakeRemote({ deploy: { code: 2, stdout: ev({ type: "error", code: "invalid", message: "bad" }) } });
    await expect(runStreaming(remote, ["deploy"], { out: () => {} })).rejects.toMatchObject({ exitCode: 2 });
  });
  test("a stream without a result is a failure", async () => {
    await expect(runStreaming(new FakeRemote({ deploy: { code: 255, stderr: "Connection refused" } }), ["deploy"], { out: () => {} }))
      .rejects.toThrow("Could not connect over SSH");
  });
});

test("requestFlags carries the whole config", () => {
  expect(requestFlags(cfg, "feat-x", "abc1234")).toEqual([
    "--project", "app", "--workspace", "feat-x", "--port", "3000", "--domain", "app.test", "--preview-domain", "preview.test",
    "--health-path", "/health", "--health-timeout", "90", "--sha", "abc1234",
  ]);
});

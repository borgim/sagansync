import { EventEmitter } from "node:events";
import { expect, test } from "vitest";
import { CliError } from "../src/lib/agent.js";
import { ConfigError } from "../src/lib/config.js";
import { exitOnBrokenPipe, report } from "../src/lib/report.js";

test("CliError prints message, details and hint and keeps its exit code", () => {
  const lines: string[] = [];
  expect(report(new CliError("deploy failed", 1, "try again", ["Last container logs:", "  boom"]), (s) => lines.push(s))).toBe(1);
  expect(lines).toEqual(["✖ deploy failed", "Last container logs:", "  boom", "try again"]);
});

test("ConfigError exits with 2, Ctrl+C in a prompt with 130, anything else with 1", () => {
  expect(report(new ConfigError("bad config"), () => {})).toBe(2);
  const abort = Object.assign(new Error("User force closed the prompt"), { name: "ExitPromptError" });
  expect(report(abort, () => {})).toBe(130);
  expect(report(new Error("boom"), () => {})).toBe(1);
});

test("a closed pipe (sagansync logs | head) exits quietly with 0", () => {
  const stdout = new EventEmitter();
  const exits: number[] = [];
  exitOnBrokenPipe(stdout, (code) => exits.push(code));
  stdout.emit("error", Object.assign(new Error("write EPIPE"), { code: "EPIPE" }));
  expect(exits).toEqual([0]);
  expect(() => stdout.emit("error", Object.assign(new Error("disk full"), { code: "ENOSPC" }))).toThrow("disk full");
});

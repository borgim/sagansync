import { expect, test } from "vitest";
import { exitCodeFor, parseEvent, Renderer } from "../src/lib/events.js";
import { ev } from "./helpers/fakeRemote.js";

test("parseEvent accepts sagand events only", () => {
  expect(parseEvent(ev({ type: "step", name: "build" }))).toMatchObject({ type: "step", name: "build" });
  expect(parseEvent('{"level":"info","msg":"app log"}')).toBeNull();
  expect(parseEvent('{"v":1,"type":"other"}')).toBeNull();
  expect(parseEvent("plain text")).toBeNull();
  expect(parseEvent("{broken")).toBeNull();
});

test("Renderer shows steps and warnings, hides logs unless verbose", () => {
  const lines: string[] = [];
  const r = new Renderer((s) => lines.push(s));
  r.line(ev({ type: "step", name: "build" }));
  r.line(ev({ type: "log", stream: "build", line: "STEP 1/3" }));
  r.line(ev({ type: "warn", code: "tls_pending", message: "certificate not ready" }));
  r.line(ev({ type: "done", url: "https://app.test" }));
  expect(lines).toEqual(["▸ Building image", "! certificate not ready"]);
  expect(r.last).toMatchObject({ type: "done", url: "https://app.test" });

  const verbose: string[] = [];
  new Renderer((s) => verbose.push(s), true).line(ev({ type: "log", line: "STEP 1/3" }));
  expect(verbose).toEqual(["  STEP 1/3"]);
});

test("exitCodeFor mirrors sagand", () => {
  expect(exitCodeFor({ v: 1, type: "done" })).toBe(0);
  expect(exitCodeFor({ v: 1, type: "error", code: "invalid" })).toBe(2);
  expect(exitCodeFor({ v: 1, type: "error", code: "busy" })).toBe(1);
});

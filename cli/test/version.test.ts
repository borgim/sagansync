import { readFileSync } from "node:fs";
import { expect, test } from "vitest";
import { VERSION } from "../src/version.js";

test("VERSION matches package.json", () => {
  const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
  expect(VERSION).toBe(pkg.version);
});

// Lowest Node version a range like ">=20.19.0" or "^20.19.0 || >=22.12.0" accepts.
function minNode(range: string): number[] {
  const mins = range.split("||").map((alt) => {
    const m = /(\d+)\.(\d+)(?:\.(\d+))?/.exec(alt);
    return m ? [Number(m[1]), Number(m[2]), Number(m[3] ?? 0)] : [0, 0, 0];
  });
  return mins.sort((a, b) => a[0]! - b[0]! || a[1]! - b[1]! || a[2]! - b[2]!)[0]!;
}
const atLeast = (a: number[], b: number[]) => a[0]! - b[0]! || a[1]! - b[1]! || a[2]! - b[2]!;

test("engines.node is at least what every dependency requires", () => {
  const read = (p: string) => JSON.parse(readFileSync(new URL(p, import.meta.url), "utf8"));
  const pkg = read("../package.json");
  const ours = minNode(pkg.engines.node);
  for (const dep of Object.keys(pkg.dependencies)) {
    const theirs = read(`../node_modules/${dep}/package.json`).engines?.node;
    if (theirs) expect(atLeast(ours, minNode(theirs)), `${dep} needs node ${theirs}`).toBeGreaterThanOrEqual(0);
  }
});

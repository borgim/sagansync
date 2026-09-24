import { readFileSync } from "node:fs";
import { expect, test } from "vitest";

const workflow = readFileSync(new URL("../../.github/workflows/release.yml", import.meta.url), "utf8");
const job = (name: string) => {
  const start = workflow.indexOf(`\n  ${name}:\n`);
  const next = workflow.slice(start + 1).search(/\n  [a-z]+:\n/);
  return workflow.slice(start, next < 0 ? undefined : start + 1 + next);
};

// Nothing reaches npm or GitHub Releases unless both test suites pass on the
// tagged commit; the CLI suite also checks that src/version.ts matches.
test("the release checks run both test suites before publishing", () => {
  const check = job("check");
  expect(check).toContain("npm test");
  expect(check).toContain("go test");
  expect(job("agent")).toContain("needs: check");
});

test("the release workflow grants read-only permissions by default", () => {
  expect(workflow).toMatch(/^permissions:\n\s+contents: read/m);
});

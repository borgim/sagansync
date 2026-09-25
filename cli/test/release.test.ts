import { readFileSync } from "node:fs";
import { expect, test } from "vitest";

const workflow = readFileSync(new URL("../../.github/workflows/release.yml", import.meta.url), "utf8");
const goreleaser = readFileSync(new URL("../../.goreleaser.yaml", import.meta.url), "utf8");
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

// npm authorizes the publish through the trusted publisher configured for
// this workflow (OIDC), which needs npm 11.5.1+, shipped with Node 24.
test("the CLI is published through trusted publishing, without a token", () => {
  const cli = job("cli");
  expect(cli).toContain("id-token: write");
  expect(cli).toMatch(/node-version: 24\b/);
  expect(workflow).not.toContain("NPM_TOKEN");
  expect(workflow).not.toContain("NODE_AUTH_TOKEN");
});

test("third-party actions are pinned to a commit", () => {
  expect(workflow).toMatch(/uses: goreleaser\/goreleaser-action@[0-9a-f]{40} /);
});

test("a pre-release tag is not published as latest", () => {
  expect(job("cli")).toMatch(/\*-\*\) tag=next/);
  expect(goreleaser).toMatch(/^\s+prerelease: auto$/m);
});

test("the npm step skips a version that is already published, so the job can be re-run", () => {
  expect(job("cli")).toMatch(/npm view "sagansync@\$version" version/);
});

import { expect, test } from "vitest";
import { dnsRecords, dnsWarning, type Lookup } from "../src/lib/dns.js";

const table = (m: Record<string, string[]>): Lookup => async (h) => {
  const r = m[h];
  if (!r) throw new Error("ENOTFOUND");
  return r;
};

test("no warning when the host points to the VPS", async () => {
  expect(await dnsWarning("app.test", "vps.test", table({ "vps.test": ["203.0.113.7"], "app.test": ["203.0.113.7"] }))).toBeNull();
});

test("warns when the host does not resolve", async () => {
  expect(await dnsWarning("app.test", "vps.test", table({ "vps.test": ["203.0.113.7"] }))).toContain("does not resolve");
});

test("warns when the host points elsewhere", async () => {
  const w = await dnsWarning("app.test", "vps.test", table({ "vps.test": ["203.0.113.7"], "app.test": ["198.51.100.1"] }));
  expect(w).toContain("198.51.100.1");
  expect(w).toContain("203.0.113.7");
});

test("stays quiet when the VPS itself cannot be resolved", async () => {
  expect(await dnsWarning("app.test", "vps.test", table({}))).toBeNull();
});

test("dnsRecords suggests one wildcard for previews", () => {
  expect(dnsRecords({ domain: "api.example.com", previewDomain: "example.com" }, "203.0.113.7")).toEqual([
    "A  api.example.com  ->  203.0.113.7",
    "A  *.example.com  ->  203.0.113.7   (one wildcard for every project's branches)",
  ]);
  expect(dnsRecords({}, "x")).toEqual([]);
});

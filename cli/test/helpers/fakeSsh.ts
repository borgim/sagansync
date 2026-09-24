import fs from "node:fs";
import os from "node:os";
import path from "node:path";

// fakeSsh writes an executable that records its argv and stdin to a JSON
// file, prints `stdout`, and exits with `code`.
export function fakeSsh(opts: { stdout?: string; code?: number; stderr?: string } = {}): { bin: string; record: () => { argv: string[]; stdin: string } } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-ssh-"));
  const out = path.join(dir, "record.json");
  const bin = path.join(dir, "ssh");
  fs.writeFileSync(bin, `#!/usr/bin/env node
const fs = require("fs");
const chunks = [];
process.stdin.on("data", (c) => chunks.push(c));
process.stdin.on("end", () => {
  fs.writeFileSync(${JSON.stringify(out)}, JSON.stringify({ argv: process.argv.slice(2), stdin: Buffer.concat(chunks).toString("utf8") }));
  process.stdout.write(${JSON.stringify(opts.stdout ?? "")});
  process.stderr.write(${JSON.stringify(opts.stderr ?? "")});
  process.exit(${opts.code ?? 0});
});
`, { mode: 0o755 });
  return { bin, record: () => JSON.parse(fs.readFileSync(out, "utf8")) };
}

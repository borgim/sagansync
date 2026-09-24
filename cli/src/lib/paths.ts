import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// packageFile resolves a file shipped with the npm package (such as
// scripts/provision.sh), both from src/ in tests and from dist/ when installed.
export function packageFile(rel: string): string {
  let dir = path.dirname(fileURLToPath(import.meta.url));
  while (!fs.existsSync(path.join(dir, "package.json"))) {
    const parent = path.dirname(dir);
    if (parent === dir) throw new Error(`cannot find the sagansync package root for ${rel}`);
    dir = parent;
  }
  return path.join(dir, rel);
}

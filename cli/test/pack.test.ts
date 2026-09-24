import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { Readable } from "node:stream";
import zlib from "node:zlib";
import { extract as tarExtract } from "tar-stream";
import { describe, expect, test } from "vitest";
import { listFiles, packProject } from "../src/lib/pack.js";

function project(files: Record<string, string>): string {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-pack-"));
  for (const [rel, body] of Object.entries(files)) {
    fs.mkdirSync(path.dirname(path.join(root, rel)), { recursive: true });
    fs.writeFileSync(path.join(root, rel), body);
  }
  return root;
}

const names = (root: string) => listFiles(root).entries.map((e) => e.rel);

describe("listFiles", () => {
  test("always excludes .git, node_modules, .sagansync and .env files", () => {
    const root = project({
      "src/index.ts": "", ".git/HEAD": "", "node_modules/x/i.js": "", "pkg/node_modules/y.js": "",
      ".sagansync/config.json": "", ".env": "SECRET=1", ".env.production": "SECRET=2", "apps/api/.env.local": "S",
      ".DS_Store": "", "Dockerfile": "",
    });
    expect(names(root)).toEqual(["Dockerfile", "apps", "apps/api", "pkg", "src", "src/index.ts"]);
  });

  test("respects .gitignore and .dockerignore", () => {
    const root = project({ ".gitignore": "dist/\n*.log\n", ".dockerignore": "coverage\n", "dist/a.js": "", "debug.log": "", "coverage/x": "", "keep.ts": "" });
    expect(names(root)).toEqual([".dockerignore", ".gitignore", "keep.ts"]);
  });

  test("a .gitignore cannot re-include .env", () => {
    const root = project({ ".gitignore": "!.env\n!.env.production\n", ".env": "SECRET=1", ".env.production": "S", "a.ts": "" });
    expect(names(root)).toEqual([".gitignore", "a.ts"]);
  });

  test("skips symlinks the agent would reject", () => {
    const root = project({ "src/a.ts": "" });
    fs.symlinkSync("src", path.join(root, "inside"));
    fs.symlinkSync("../shared", path.join(root, "up"));
    fs.symlinkSync("/etc/passwd", path.join(root, "abs"));
    const { entries, skipped } = listFiles(root);
    expect(entries.map((e) => e.rel)).toEqual(["inside", "src", "src/a.ts"]);
    expect(skipped).toEqual(["abs -> /etc/passwd", "up -> ../shared"]);
  });
});

async function readArchive(stream: Readable): Promise<Record<string, { type: string; body: string; link?: string; mode?: number }>> {
  const extract = tarExtract();
  const out: Record<string, { type: string; body: string; link?: string; mode?: number }> = {};
  const done = new Promise<void>((resolve, reject) => {
    extract.on("entry", (header, body, next) => {
      const chunks: Buffer[] = [];
      body.on("data", (c) => chunks.push(c as Buffer));
      body.on("end", () => {
        out[header.name] = { type: header.type ?? "file", body: Buffer.concat(chunks).toString(), link: header.linkname ?? undefined, mode: header.mode };
        next();
      });
      body.resume();
    });
    extract.on("finish", resolve);
    extract.on("error", reject);
  });
  stream.pipe(zlib.createGunzip()).pipe(extract);
  await done;
  return out;
}

describe("packProject", () => {
  test("produces a tar.gz with files, dirs, symlinks and modes", async () => {
    const root = project({ "Dockerfile": "FROM node", "src/index.ts": "console.log(1)" });
    fs.chmodSync(path.join(root, "Dockerfile"), 0o755);
    fs.symlinkSync("src", path.join(root, "lib"));
    const archive = await readArchive(packProject(listFiles(root).entries));
    expect(archive["Dockerfile"]).toMatchObject({ type: "file", body: "FROM node", mode: 0o755 });
    expect(archive["src"]?.type).toBe("directory");
    expect(archive["src/index.ts"]?.body).toBe("console.log(1)");
    expect(archive["lib"]).toMatchObject({ type: "symlink", link: "src" });
  });

  test("a file removed while packing fails the stream", async () => {
    const root = project({ "a.txt": "hello" });
    const { entries } = listFiles(root);
    fs.rmSync(path.join(root, "a.txt"));
    const stream = packProject(entries);
    await expect(new Promise((resolve, reject) => { stream.on("error", reject); stream.on("end", resolve); stream.resume(); }))
      .rejects.toThrow("packing the project failed");
  });
});

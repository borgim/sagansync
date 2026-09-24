import fs from "node:fs";
import path from "node:path";
import type { Readable } from "node:stream";
import zlib from "node:zlib";
import ignore from "ignore";
import { pack as tarPack, type Pack } from "tar-stream";

// Never uploaded, whatever .gitignore or .dockerignore say (they cannot
// re-include these with "!" rules). Secrets in .env files are set with
// `sagansync env` instead.
const ALWAYS = [".git", "node_modules", ".sagansync", ".env*", ".DS_Store"];

// Always uploaded: Podman needs the build file in the context, and it
// applies .dockerignore itself. Listing them in .dockerignore is a common
// Docker pattern that must not break the build.
const KEEP = new Set(["Dockerfile", "Containerfile", ".dockerignore"]);

export type Matcher = (rel: string, isDir: boolean) => boolean;

// ignoreMatcher applies the fixed exclusions plus the project's root
// .gitignore and .dockerignore (both read with gitignore rules).
export function ignoreMatcher(root: string): Matcher {
  const always = ignore().add(ALWAYS);
  const project = ignore();
  for (const file of [".gitignore", ".dockerignore"]) {
    const p = path.join(root, file);
    if (fs.existsSync(p)) project.add(fs.readFileSync(p, "utf8"));
  }
  return (rel, isDir) => {
    if (!isDir && KEEP.has(rel)) return false;
    const p = isDir ? `${rel}/` : rel;
    return always.ignores(p) || project.ignores(p);
  };
}

export type Entry = {
  rel: string;
  abs: string;
  type: "file" | "dir" | "symlink";
  mode: number;
  size: number;
  mtime: Date;
  link?: string;
};

export type FileList = { entries: Entry[]; skipped: string[] };

// sagand rejects archives with symlinks that could point outside the release.
function unsafeLink(target: string): boolean {
  return target.startsWith("/") || target.split("/").includes("..");
}

// listFiles walks root and returns what will be uploaded, in a stable order.
// Symlinks the agent would refuse are skipped and reported instead of failing
// the whole deploy; sockets and other special files are ignored.
export function listFiles(root: string, match: Matcher = ignoreMatcher(root)): FileList {
  const entries: Entry[] = [];
  const skipped: string[] = [];
  const walk = (dirRel: string) => {
    const names = fs.readdirSync(path.join(root, dirRel)).sort();
    for (const name of names) {
      const rel = dirRel ? `${dirRel}/${name}` : name;
      const abs = path.join(root, rel);
      const st = fs.lstatSync(abs);
      const base = { rel, abs, mode: st.mode & 0o777, mtime: st.mtime, size: 0 };
      if (st.isDirectory()) {
        if (match(rel, true)) continue;
        entries.push({ ...base, type: "dir" });
        walk(rel);
      } else if (st.isSymbolicLink()) {
        if (match(rel, false)) continue;
        const link = fs.readlinkSync(abs);
        if (unsafeLink(link)) skipped.push(`${rel} -> ${link}`);
        else entries.push({ ...base, type: "symlink", link });
      } else if (st.isFile()) {
        if (match(rel, false)) continue;
        entries.push({ ...base, type: "file", size: st.size });
      }
    }
  };
  walk("");
  return { entries, skipped };
}

type EntryHeader = Parameters<Pack["entry"]>[0];

function addEntry(pack: Pack, header: EntryHeader, file?: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const done = (err?: Error | null) => (err ? reject(err) : resolve());
    if (!file) {
      pack.entry(header, Buffer.alloc(0), done);
      return;
    }
    const sink = pack.entry(header, done);
    sink.on("error", () => {}); // reported through done or the read stream
    const src = fs.createReadStream(file);
    src.on("error", reject);
    src.pipe(sink);
  });
}

// packProject streams the entries as a gzip-compressed tar archive.
export function packProject(entries: Entry[]): Readable {
  const pack = tarPack();
  const gz = zlib.createGzip();
  const fail = (err: Error) => {
    const msg = err.message === "Size mismatch" ? "a file changed while it was being packed; try again" : err.message;
    gz.destroy(new Error(`packing the project failed: ${msg}`));
  };
  pack.on("error", fail);
  pack.pipe(gz);
  (async () => {
    for (const e of entries) {
      const header = { name: e.rel, mode: e.mode, mtime: e.mtime };
      if (e.type === "dir") await addEntry(pack, { ...header, type: "directory" });
      else if (e.type === "symlink") await addEntry(pack, { ...header, type: "symlink", linkname: e.link });
      else await addEntry(pack, { ...header, type: "file", size: e.size }, e.abs);
    }
    pack.finalize();
  })().catch((err: Error) => {
    fail(err);
    pack.destroy();
  });
  return gz;
}

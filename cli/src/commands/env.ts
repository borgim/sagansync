import fs from "node:fs";
import { CliError, failure, workspaceFlags } from "../lib/agent.js";
import { resolveWorkspace } from "../lib/git.js";
import { paint } from "../lib/style.js";
import { type Ctx, preflight } from "./context.js";

const KEY_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

export type EnvOptions = { workspace?: string };

const restIsComment = (rest: string) => {
  const r = rest.trim();
  return r === "" || r.startsWith("#");
};

// parseValue follows dotenv: double quotes allow \n, \t and \" escapes,
// single quotes are literal, and in unquoted values " #" starts a comment.
// It returns null when a quote is never closed or text follows it.
function parseValue(raw: string): string | null {
  const v = raw.trim();
  if (v.startsWith('"')) {
    let out = "";
    let i = 1;
    for (; i < v.length && v[i] !== '"'; i++) {
      if (v[i] === "\\" && i + 1 < v.length) {
        const next = v[++i]!;
        out += next === "n" ? "\n" : next === "t" ? "\t" : next === "r" ? "\r" : next;
      } else {
        out += v[i];
      }
    }
    return i < v.length && restIsComment(v.slice(i + 1)) ? out : null;
  }
  if (v.startsWith("'")) {
    const end = v.indexOf("'", 1);
    return end > 0 && restIsComment(v.slice(end + 1)) ? v.slice(1, end) : null;
  }
  const comment = v.search(/\s#/);
  return (comment >= 0 ? v.slice(0, comment) : v).trim();
}

// parseDotenv reads KEY=VALUE lines. Blank lines and # comments are skipped
// and an optional "export " prefix is allowed. Multi-line values are not
// supported and are reported as errors instead of being guessed.
export function parseDotenv(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  text.split(/\r?\n/).forEach((raw, i) => {
    const line = raw.trim();
    if (!line || line.startsWith("#")) return;
    const m = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$/.exec(line);
    const value = m ? parseValue(m[2] ?? "") : null;
    if (!m || value === null) throw new CliError(`Line ${i + 1} is not KEY=VALUE: ${raw}`, 2);
    out[m[1]!] = value;
  });
  return out;
}

export function parsePairs(pairs: string[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of pairs) {
    const i = p.indexOf("=");
    const key = i > 0 ? p.slice(0, i) : "";
    if (!KEY_RE.test(key)) throw new CliError(`Expected KEY=VALUE, got "${p}".`, 2);
    out[key] = p.slice(i + 1);
  }
  return out;
}

export async function envSet(ctx: Ctx, pairs: string[], opts: EnvOptions & { file?: string }): Promise<void> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  const kv = { ...(opts.file ? parseDotenv(fs.readFileSync(opts.file, "utf8")) : {}), ...parsePairs(pairs) };
  const keys = Object.keys(kv);
  if (keys.length === 0) throw new CliError("Nothing to set. Pass KEY=VALUE pairs or --file <path>.", 2);
  await preflight(ctx);
  // Values travel on stdin, never on the remote command line.
  const r = await ctx.remote.run(["env", "set", ...workspaceFlags(ctx.config, workspace)], JSON.stringify(kv));
  if (r.code !== 0) throw failure(r);
  ctx.out(paint("green", `✔ Set ${keys.sort().join(", ")} on ${workspace}.`) + " Redeploy to apply: sagansync deploy");
}

export async function envUnset(ctx: Ctx, keys: string[], opts: EnvOptions): Promise<void> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  if (keys.length === 0) throw new CliError("Pass the names of the variables to remove.", 2);
  await preflight(ctx);
  const r = await ctx.remote.run(["env", "unset", ...workspaceFlags(ctx.config, workspace), ...keys]);
  if (r.code !== 0) throw failure(r);
  ctx.out(paint("green", `✔ Removed ${keys.join(", ")} from ${workspace}.`) + " Redeploy to apply: sagansync deploy");
}

export async function envList(ctx: Ctx, opts: EnvOptions): Promise<void> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  await preflight(ctx);
  const r = await ctx.remote.run(["env", "list", ...workspaceFlags(ctx.config, workspace)]);
  if (r.code !== 0) throw failure(r);
  const keys = (JSON.parse(r.stdout) as { keys: string[] }).keys;
  if (keys.length === 0) ctx.out(`No variables set on ${workspace}.`);
  for (const k of keys) ctx.out(`${k}=********`);
}

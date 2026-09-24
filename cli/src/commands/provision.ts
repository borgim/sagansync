import { createHash } from "node:crypto";
import fs from "node:fs";
import { gunzipSync } from "node:zlib";
import { extract as tarExtract, pack as tarPack } from "tar-stream";
import { checkAgent, CliError, sshError } from "../lib/agent.js";
import { type Config, keyPath, knownHostsPath } from "../lib/config.js";
import { packageFile } from "../lib/paths.js";
import { type AdminTarget, type Shell, shQuote } from "../lib/ssh.js";
import { paint } from "../lib/style.js";
import { VERSION } from "../version.js";
import { type Ctx, warn } from "./context.js";

export type ProvisionOptions = {
  admin?: string;
  adminKey?: string;
  upgrade?: boolean;
  agentBinary?: string;
  acmeEmail?: string;
  acmeCa?: string;
  acmeRootCa?: string;
  removeCaddy?: boolean;
};

export const RELEASES = "https://github.com/borgim/sagansync/releases/download";

// adminTarget turns --admin (user@host, or just user; default root) into the
// account provision logs in with. The host defaults to the config's.
export function adminTarget(cfg: Config, opts: Pick<ProvisionOptions, "admin" | "adminKey">): AdminTarget {
  const value = opts.admin ?? "root";
  const at = value.lastIndexOf("@");
  const user = at >= 0 ? value.slice(0, at) : value;
  const host = at >= 0 ? value.slice(at + 1) : cfg.host;
  if (!/^[A-Za-z_][A-Za-z0-9_.-]{0,31}$/.test(user) || !/^[A-Za-z0-9][A-Za-z0-9.:-]*$/.test(host)) {
    throw new CliError(`Invalid --admin "${value}": use user@host, for example root@203.0.113.7.`, 2);
  }
  return { host, port: cfg.sshPort, user, identityFile: opts.adminKey, knownHosts: knownHostsPath() };
}

const ARCHES: Record<string, { name: "amd64" | "arm64"; elfMachine: number }> = {
  x86_64: { name: "amd64", elfMachine: 0x3e },
  aarch64: { name: "arm64", elfMachine: 0xb7 },
  arm64: { name: "arm64", elfMachine: 0xb7 },
};

export function archFor(uname: string): { name: "amd64" | "arm64"; elfMachine: number } {
  const arch = ARCHES[uname.trim()];
  if (!arch) throw new CliError(`The server's architecture "${uname.trim()}" is not supported: sagand is built for x86_64 and arm64.`);
  return arch;
}

// checkBinary makes sure a local --agent-binary is a Linux executable for the
// server's CPU before anything is installed.
export function checkBinary(bin: Buffer, arch: { name: string; elfMachine: number }): void {
  const isElf = bin.length > 20 && bin.readUInt32BE(0) === 0x7f454c46;
  if (!isElf) throw new CliError("--agent-binary is not a Linux executable. Build it with GOOS=linux.", 2);
  if (bin.readUInt16LE(18) !== arch.elfMachine) {
    throw new CliError(`--agent-binary was built for another CPU; the server is ${arch.name}. Build it with GOARCH=${arch.name}.`, 2);
  }
}

async function extractFile(tgz: Buffer, name: string): Promise<Buffer> {
  const ex = tarExtract();
  let found: Buffer | undefined;
  const done = new Promise<void>((resolve, reject) => {
    ex.on("entry", (header, body, next) => {
      const chunks: Buffer[] = [];
      body.on("data", (c) => chunks.push(c as Buffer));
      body.on("end", () => {
        if (header.name.replace(/^\.\//, "") === name) found = Buffer.concat(chunks);
        next();
      });
    });
    ex.on("finish", resolve);
    ex.on("error", reject);
  });
  ex.end(gunzipSync(tgz));
  await done;
  if (!found) throw new CliError(`The release archive does not contain ${name}.`);
  return found;
}

// downloadAgent fetches the sagand release matching this CLI and checks it
// against the release's checksums.txt before it is used.
export async function downloadAgent(arch: string, fetchFn: typeof fetch = fetch, version = VERSION): Promise<Buffer> {
  const base = `${RELEASES}/v${version}`;
  const name = `sagand_${version}_linux_${arch}.tar.gz`;
  const get = async (file: string) => {
    let res: Response;
    try {
      res = await fetchFn(`${base}/${file}`);
    } catch (err) {
      throw new CliError(`Could not download ${base}/${file}: ${(err as Error).message}`, 1, "Check your connection, or pass --agent-binary with a sagand built for the server.");
    }
    if (!res.ok) throw new CliError(`Could not download ${base}/${file} (HTTP ${res.status}).`, 1, "Pass --agent-binary with a sagand built for the server.");
    return Buffer.from(await res.arrayBuffer());
  };
  const sums = (await get("checksums.txt")).toString("utf8");
  const expected = sums.split("\n").map((l) => l.trim().split(/\s+/)).find((parts) => parts[1] === name)?.[0];
  if (!expected) throw new CliError(`checksums.txt of v${version} has no entry for ${name}.`);
  const archive = await get(name);
  const actual = createHash("sha256").update(archive).digest("hex");
  if (actual !== expected) {
    throw new CliError(`Checksum mismatch for ${name}: expected ${expected}, got ${actual}. Nothing was installed.`, 1, "The download is corrupt or was tampered with. Try again later.");
  }
  return extractFile(archive, "sagand");
}

export type BundleFile = { name: string; data: Buffer | string; mode?: number };

// bundle packs the files provision.sh needs into an uncompressed tar.
export async function bundle(files: BundleFile[]): Promise<Buffer> {
  const pack = tarPack();
  const chunks: Buffer[] = [];
  pack.on("data", (c) => chunks.push(c as Buffer));
  const done = new Promise<void>((resolve, reject) => {
    pack.on("end", resolve);
    pack.on("error", reject);
  });
  for (const f of files) pack.entry({ name: f.name, mode: f.mode ?? 0o644 }, f.data);
  pack.finalize();
  await done;
  return Buffer.concat(chunks);
}

export function daemonSettings(opts: ProvisionOptions): string {
  const cfg: Record<string, string> = {};
  if (opts.acmeEmail) cfg.acmeEmail = opts.acmeEmail;
  if (opts.acmeCa) cfg.acmeCA = opts.acmeCa;
  if (opts.acmeRootCa) cfg.acmeRootCA = "/var/lib/sagand/acme-root-ca.pem";
  return JSON.stringify(cfg, null, 2) + "\n";
}

// remoteScript unpacks the bundle into a temporary directory and runs
// provision.sh as root (directly, or through passwordless sudo). It runs under
// sh whatever the admin's login shell is.
export function remoteScript(flags: string[]): string {
  const args = flags.map(shQuote).join(" ");
  const script = `set -e; d=$(mktemp -d); trap 'rm -rf "$d"' EXIT; tar -xf - -C "$d"; ` +
    `if [ "$(id -u)" -eq 0 ]; then bash "$d/provision.sh" "$d" ${args}; else sudo -n bash "$d/provision.sh" "$d" ${args}; fi`;
  return `sh -c ${shQuote(script)}`;
}

function provisionError(stderr: string): CliError {
  const text = stderr.trim();
  // Classic sudo, sudo-rs (default on Ubuntu 25.10+) and servers without sudo.
  if (/sudo: (a password is required|a terminal is required|interactive authentication is required)|sudo: (command )?not found/.test(text)) {
    return new CliError("The admin account needs root or passwordless sudo.", 1, "Use --admin root@<host>, or allow passwordless sudo for that user.");
  }
  const last = text.split("\n").filter((l) => l.startsWith("✖")).pop();
  return new CliError(last ? last.replace(/^✖\s*/, "") : `Provisioning failed: ${text || "unknown error"}`, 1, undefined,
    last ? text.split("\n").filter((l) => l !== last) : []);
}

// provision installs or upgrades sagand on the VPS with an admin account, then
// checks that the deploy key reaches it.
export async function provision(ctx: Ctx, opts: ProvisionOptions, deps: { shell: Shell; fetch?: typeof fetch }): Promise<void> {
  const pub = `${keyPath(ctx.config)}.pub`;
  if (!fs.existsSync(pub)) throw new CliError(`The deploy key ${pub} does not exist.`, 1, "Run `sagansync init` to create it.");
  if (opts.acmeRootCa && !fs.existsSync(opts.acmeRootCa)) throw new CliError(`${opts.acmeRootCa} does not exist.`, 2);

  const uname = await deps.shell.run("uname -m");
  if (uname.code === 255) throw sshError(uname.stderr);
  if (uname.code !== 0) throw new CliError(`Could not inspect the server: ${uname.stderr.trim()}`);
  const arch = archFor(uname.stdout);

  let agent: Buffer;
  if (opts.agentBinary) {
    agent = fs.readFileSync(opts.agentBinary);
    checkBinary(agent, arch);
  } else {
    ctx.out(`Downloading sagand ${VERSION} for linux/${arch.name}...`);
    agent = await downloadAgent(arch.name, deps.fetch);
  }

  const files: BundleFile[] = [
    { name: "provision.sh", data: fs.readFileSync(packageFile("scripts/provision.sh")), mode: 0o755 },
    { name: "sagand", data: agent, mode: 0o755 },
    { name: "deploy.pub", data: fs.readFileSync(pub) },
    { name: "config.json", data: daemonSettings(opts) },
  ];
  if (opts.acmeRootCa) files.push({ name: "acme-root-ca.pem", data: fs.readFileSync(opts.acmeRootCa) });
  const flags = [...(opts.upgrade ? ["--upgrade"] : []), ...(opts.removeCaddy ? ["--remove-caddy"] : [])];

  const r = await deps.shell.stream(remoteScript(flags), (line) => ctx.out(line), await bundle(files));
  if (r.code === 255) throw sshError(r.stderr);
  if (r.code !== 0) throw provisionError(r.stderr);

  const warning = await checkAgent(ctx.remote);
  if (warning) warn(ctx, warning);
  ctx.out(paint("green", `✔ ${ctx.config.host} is ready. Next: sagansync deploy`));
}

import { spawn } from "node:child_process";

export type SSHOptions = {
  host: string;
  user?: string;              // ex.: "ubuntu"
  port?: number;              // default: 22
  identityFile?: string;      // ex.: "~/.ssh/id_ed25519"
  strictHostKeyChecking?: "yes" | "no" | "accept-new";
  userKnownHostsFile?: string;
  extraSSHOptions?: string[];
};

export type SSHExecOptions = {
  command: string;
  stdin?: string;
  remoteEnv?: Record<string, string | number | boolean | undefined | null>;
  allocatePty?: boolean;
  captureOutput?: boolean; // default: false (herda stdio)
  timeoutMs?: number; // default: undefined (sem timeout)
};

export type SSHExecResult = {
  code: number;
  stdout?: string;
  stderr?: string;
};

export type SCPUploadOptions = SSHOptions & {
  recursive?: boolean; // default: true
};

function buildUserAtHost(opts: SSHOptions): string {
  if (opts.user) return `${opts.user}@${opts.host.replace(/^.*@/, "")}`;
  return opts.host;
}

function buildCommonSSHArgs(opts: SSHOptions): string[] {
  const args: string[] = [];
  args.push("-p", String(opts.port ?? 22));
  if (opts.identityFile) args.push("-i", opts.identityFile);

  const strict = opts.strictHostKeyChecking ?? "no";
  args.push("-o", `StrictHostKeyChecking=${strict}`);

  if (strict === "no") {
    args.push("-o", `UserKnownHostsFile=${opts.userKnownHostsFile ?? "/dev/null"}`);
  } else if (opts.userKnownHostsFile) {
    args.push("-o", `UserKnownHostsFile=${opts.userKnownHostsFile}`);
  }

  if (opts.extraSSHOptions?.length) {
    args.push(...opts.extraSSHOptions);
  }

  return args;
}

function shQuote(value: string): string {
  return `'${value.replace(/'/g, `'\"'\"'`)}'`;
}

function buildRemoteEnvPrefix(env?: SSHExecOptions["remoteEnv"]): string {
  if (!env) return "";
  const pairs: string[] = [];
  for (const [k, v] of Object.entries(env)) {
    if (v === undefined || v === null) continue;
    pairs.push(`${k}=${shQuote(String(v))}`);
  }
  return pairs.length ? pairs.join(" ") + " " : "";
}

export function sshExec(
  opts: SSHOptions,
  exec: SSHExecOptions
): Promise<SSHExecResult> {
  return new Promise((resolve, reject) => {
    const target = buildUserAtHost(opts);
    const sshArgs = buildCommonSSHArgs(opts);

    // -T: disable pseudo-tty allocation (default). -tt: force TTY allocation
    if (exec.allocatePty) sshArgs.push("-tt");

    const envPrefix = buildRemoteEnvPrefix(exec.remoteEnv);
    const remoteCmd = envPrefix + exec.command;

    sshArgs.push(target, remoteCmd);

    const stdio: any = exec.captureOutput ? ["pipe", "pipe", "pipe"] : ["pipe", "inherit", "inherit"];
    const child = spawn("ssh", sshArgs, { stdio });

    let stdout = "";
    let stderr = "";
    let timedOut = false;
    let timer: NodeJS.Timeout | undefined;

    if (exec.captureOutput) {
      child.stdout?.on("data", (d) => (stdout += String(d)));
      child.stderr?.on("data", (d) => (stderr += String(d)));
    }

    if (exec.stdin) {
      child.stdin?.write(exec.stdin);
    }
    child.stdin?.end();

    if (exec.timeoutMs && exec.timeoutMs > 0) {
      timer = setTimeout(() => {
        timedOut = true;
        child.kill("SIGTERM");
        setTimeout(() => child.kill("SIGKILL"), 2000);
      }, exec.timeoutMs);
    }

    child.on("exit", (code) => {
      if (timer) clearTimeout(timer);
      if (code === 0 && !timedOut) {
        return resolve({ code: 0, stdout: exec.captureOutput ? stdout : undefined, stderr: exec.captureOutput ? stderr : undefined });
      }
      const err = new Error(
        timedOut
          ? `SSH command timed out after ${exec.timeoutMs} ms`
          : `SSH command failed with code ${code}`
      );
      (err as any).code = code;
      (err as any).stdout = stdout;
      (err as any).stderr = stderr;
      reject(err);
    });
  });
}

export function scpUpload(
  localPath: string,
  remotePath: string,
  opts: SCPUploadOptions
): Promise<{ code: number }> {
  return new Promise((resolve, reject) => {
    const target = buildUserAtHost(opts);
    const args: string[] = [];

    args.push("-P", String(opts.port ?? 22));
    if (opts.identityFile) args.push("-i", opts.identityFile);

    const strict = opts.strictHostKeyChecking ?? "no";
    args.push("-o", `StrictHostKeyChecking=${strict}`);
    if (strict === "no") {
      args.push("-o", `UserKnownHostsFile=${opts.userKnownHostsFile ?? "/dev/null"}`);
    } else if (opts.userKnownHostsFile) {
      args.push("-o", `UserKnownHostsFile=${opts.userKnownHostsFile}`);
    }

    if (opts.recursive ?? true) args.push("-r");
    args.push(localPath, `${target}:${remotePath}`);

    const child = spawn("scp", args, { stdio: "inherit" });
    child.on("exit", (code) => {
      if (code === 0) return resolve({ code: 0 });
      reject(new Error(`SCP upload failed with code ${code}`));
    });
  });
}

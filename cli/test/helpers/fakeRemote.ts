import type { Readable } from "node:stream";
import type { Input, Remote, RunResult } from "../../src/lib/ssh.js";

export type Call = { args: string[]; stdin: string };
type Reply = Partial<RunResult> | ((call: Call) => Partial<RunResult>);

async function read(input?: Input): Promise<string> {
  if (input === undefined) return "";
  if (typeof input === "string") return input;
  if (Buffer.isBuffer(input)) return input.toString("latin1");
  const chunks: Buffer[] = [];
  for await (const c of input as Readable) chunks.push(Buffer.from(c));
  return Buffer.concat(chunks).toString("latin1");
}

// FakeRemote answers sagand subcommands from a table keyed by the first
// argument (or "env set" style pairs) and records every call.
export class FakeRemote implements Remote {
  calls: Call[] = [];
  constructor(private readonly replies: Record<string, Reply> = {}) {}

  private reply(call: Call): RunResult {
    const key2 = call.args.slice(0, 2).join(" ");
    const r = this.replies[key2] ?? this.replies[call.args[0] ?? ""] ?? {};
    const v = typeof r === "function" ? r(call) : r;
    return { code: v.code ?? 0, stdout: v.stdout ?? "", stderr: v.stderr ?? "" };
  }

  async run(args: string[], stdin?: Input): Promise<RunResult> {
    const call = { args, stdin: await read(stdin) };
    this.calls.push(call);
    return this.reply(call);
  }

  async stream(args: string[], onLine: (line: string) => void, stdin?: Input) {
    const r = await this.run(args, stdin);
    for (const line of r.stdout.split("\n")) if (line) onLine(line);
    return { code: r.code, stderr: r.stderr };
  }

  argsOf(cmd: string): string[] | undefined {
    return this.calls.find((c) => c.args[0] === cmd)?.args;
  }
}

export const ev = (e: Record<string, unknown>) => JSON.stringify({ v: 1, ...e });
export const VERSION_OK = { stdout: '{"version":"0.1.0","protocol":1}\n' };

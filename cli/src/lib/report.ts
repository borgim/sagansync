import { CliError } from "./agent.js";
import { ConfigError } from "./config.js";
import { paint } from "./style.js";

// report prints an error for the user and returns the process exit code.
export function report(err: unknown, write: (line: string) => void = console.error): number {
  if (err instanceof Error && err.name === "ExitPromptError") return 130; // Ctrl+C in a prompt
  if (err instanceof CliError) {
    write(paint("red", `✖ ${err.message}`));
    for (const d of err.details) write(d);
    if (err.hint) write(paint("yellow", err.hint));
    return err.exitCode;
  }
  if (err instanceof ConfigError) {
    write(paint("red", `✖ ${err.message}`));
    return 2;
  }
  write(paint("red", `✖ ${err instanceof Error ? err.message : String(err)}`));
  return 1;
}

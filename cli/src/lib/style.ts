import { styleText } from "node:util";

type Format = Parameters<typeof styleText>[0];

// paint colors text only when stdout is a terminal and NO_COLOR is unset.
export function paint(format: Format, text: string): string {
  if (!process.stdout.isTTY || process.env.NO_COLOR) return text;
  return styleText(format, text);
}

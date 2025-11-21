import { execSync } from "node:child_process";

export function getCurrentBranch(): string | null {
  try {
    return execSync("git rev-parse --abbrev-ref HEAD", { stdio: "pipe" })
      .toString()
      .trim();
  } catch (e) {
    return null;
  }
}

export function sanitizeWorkspaceName(branch: string): string {
  let name = branch.toLowerCase().replace(/[^a-z0-9-]/g, "-");
  name = name.replace(/-+/g, "-").replace(/^-|-$/g, "");

  if (name === "main" || name === "master") return "production";
  if (name === "develop" || name === "dev") return "staging";

  return name;
}
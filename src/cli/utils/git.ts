import { execSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

export function getCurrentBranch(): string | null {
  try {
    const branch = execSync("git symbolic-ref --short HEAD", { 
      stdio: ["ignore", "pipe", "ignore"],
      encoding: "utf8"
    });
    return branch.trim();
    
  } catch (e) {
    try {
      const branch = execSync("git rev-parse --abbrev-ref HEAD", { 
        stdio: ["ignore", "pipe", "ignore"],
        encoding: "utf8"
      });
      return branch.trim();
    } catch (e2: any) {
      return null;
    }
  }
}

export function sanitizeWorkspaceName(branch: string): string {
  let name = branch.toLowerCase().replace(/[^a-z0-9-]/g, "-");
  name = name.replace(/-+/g, "-").replace(/^-|-$/g, "");

  if (name === "main" || name === "master") return "production";
  if (name === "develop" || name === "dev") return "staging";

  return name;
}

export function getGitHeadPath(): string | null {
  const gitDir = path.join(process.cwd(), ".git");
  if (fs.existsSync(gitDir)) {
    return path.join(gitDir, "HEAD");
  }
  return null;
}
import readline from "node:readline";
import fs from "node:fs";
import chalk from "chalk";

type AskOptions = {
  defaultValue?: string;
  required?: boolean;
};

export async function initCommand() {
  const rl = readline.createInterface({
    input: process.stdin,
    output: process.stdout,
  });

  const ask = (
    question: string,
    config: AskOptions = {}
  ): Promise<string> =>
    new Promise<string>((resolve) => {
      const q = config.defaultValue
        ? `${chalk.white.bold(question)} ${chalk.cyan.dim(`(${config.defaultValue})`)}: `
        : `${chalk.white.bold(question)}: `;

      const askLoop = () => {
        rl.question(q, (answer) => {
          const finalAnswer = answer.trim() || config.defaultValue || "";

          if (config && config.required && !finalAnswer) {
            console.log(chalk.red("❌ This field is required."));
            return askLoop();
          }

          resolve(finalAnswer);
        });
      };

      askLoop();
    });

  const host = await ask("VPS host (eg: user@1.2.3.4)", { required: true });
  const remotePath = await ask("Remote path", { defaultValue: "/srv/myapp" });
  const port = await ask("App port", { defaultValue: "3000" });
  const domain = await ask("Domain");

  rl.close();

  const config = { host, remotePath, port: parseInt(port), domain };
  fs.mkdirSync(".sagansync", { recursive: true });
  fs.writeFileSync(".sagansync/config.json", JSON.stringify(config, null, 2));

  console.log(`${chalk.green("✅ Config saved to")} ${chalk.white.bold(".sagansync/config.json")}`);
}

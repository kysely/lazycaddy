export type CaddyValidationResult = {
  ok: boolean;
  skipped?: boolean;
  command: string[];
  stdout: string;
  stderr: string;
  output: string;
  error?: string;
  exitCode?: number;
  ranAt: Date;
  durationMs: number;
};

export async function validateCaddyConfig(
  configPath: string | undefined,
  adapter?: string,
  timeoutMs = 10000,
): Promise<CaddyValidationResult> {
  const ranAt = new Date();
  const startedAt = performance.now();

  if (!configPath) {
    return {
      ok: false,
      skipped: true,
      command: [],
      stdout: "",
      stderr: "",
      output: "No config path was discovered for the running Caddy service.",
      error: "No config path discovered.",
      ranAt,
      durationMs: 0,
    };
  }

  const command = ["caddy", "validate", "--config", configPath];
  if (adapter) command.push("--adapter", adapter);

  try {
    const process = Bun.spawn(command, {
      stdout: "pipe",
      stderr: "pipe",
    });

    const timeout = setTimeout(() => process.kill(), timeoutMs);
    const [stdout, stderr, exitCode] = await Promise.all([
      new Response(process.stdout).text(),
      new Response(process.stderr).text(),
      process.exited,
    ]).finally(() => clearTimeout(timeout));
    const durationMs = Math.round(performance.now() - startedAt);
    const output = [stdout.trim(), stderr.trim()].filter(Boolean).join("\n");

    return {
      ok: exitCode === 0,
      command,
      stdout: stdout.trim(),
      stderr: stderr.trim(),
      output: output || (exitCode === 0 ? "Config is valid." : `caddy validate exited with code ${exitCode}`),
      error: exitCode === 0 ? undefined : stderr.trim() || stdout.trim() || `caddy validate exited with code ${exitCode}`,
      exitCode,
      ranAt,
      durationMs,
    };
  } catch (error) {
    const durationMs = Math.round(performance.now() - startedAt);
    const message = error instanceof Error ? error.message : String(error);

    return {
      ok: false,
      command,
      stdout: "",
      stderr: message,
      output: `Could not run caddy validate: ${message}`,
      error: message,
      ranAt,
      durationMs,
    };
  }
}

import { type CaddySource, sourceLabel } from "./caddyConfig.ts";

export type CaddyLogsResult = {
  ok: boolean;
  command: string[];
  output: string;
  error?: string;
  fetchedAt: Date;
  durationMs: number;
  exitCode?: number;
};

export type CaddyAccessLogsResult = {
  ok: boolean;
  available: boolean;
  sourceLabel?: string;
  output: string;
  error?: string;
  fetchedAt: Date;
  durationMs: number;
  details: string[];
};

export async function fetchCaddyLogs(lines = 100, timeoutMs = 2500): Promise<CaddyLogsResult> {
  const command = ["journalctl", "-u", "caddy", "-n", String(lines), "--no-pager", "-o", "short-iso"];
  const fetchedAt = new Date();
  const startedAt = performance.now();

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

    if (exitCode !== 0) {
      return {
        ok: false,
        command,
        output: stdout.trim(),
        error: stderr.trim() || stdout.trim() || `journalctl exited with code ${exitCode}`,
        fetchedAt,
        durationMs,
        exitCode,
      };
    }

    return {
      ok: true,
      command,
      output: stdout.trim() || "No caddy.service logs found.",
      fetchedAt,
      durationMs,
      exitCode,
    };
  } catch (error) {
    const durationMs = Math.round(performance.now() - startedAt);
    const message = error instanceof Error ? error.message : String(error);

    return {
      ok: false,
      command,
      output: "",
      error: `Could not run journalctl: ${message}`,
      fetchedAt,
      durationMs,
    };
  }
}

export async function fetchAccessLogsForSource(
  source: CaddySource | undefined,
  serviceLogs: CaddyLogsResult | undefined,
  lines = 100,
): Promise<CaddyAccessLogsResult> {
  const fetchedAt = new Date();
  const startedAt = performance.now();

  if (!source) {
    return {
      ok: false,
      available: false,
      output: "No source selected.",
      fetchedAt,
      durationMs: 0,
      details: [],
    };
  }

  const label = sourceLabel(source);

  if (source.accessLogs.length === 0) {
    return {
      ok: false,
      available: false,
      sourceLabel: label,
      output: `Access logs are not configured for ${label}.`,
      error: "No access log writer is configured for this source in the active Caddy config.",
      fetchedAt,
      durationMs: Math.round(performance.now() - startedAt),
      details: ["No access log writer found in apps.http.servers.*.logs for this source."],
    };
  }

  const chunks: string[] = [];
  const errors: string[] = [];
  const details = source.accessLogs.map((log) => {
    const target = log.filename ? `${log.writerOutput}:${log.filename}` : log.writerOutput;
    return `${log.source} -> ${log.loggerName} (${target}${log.encoder ? `, ${log.encoder}` : ""})`;
  });

  for (const accessLog of source.accessLogs) {
    if (accessLog.writerOutput === "file" && accessLog.filename) {
      const fileResult = await tailFile(accessLog.filename, lines);
      if (fileResult.ok) {
        chunks.push(header(accessLog.filename), fileResult.output);
      } else {
        errors.push(fileResult.error || `Could not read ${accessLog.filename}`);
      }
      continue;
    }

    if (["stdout", "stderr", "default"].includes(accessLog.writerOutput)) {
      const filtered = filterServiceLogsForSource(serviceLogs?.output || "", source, accessLog);
      chunks.push(
        header(`${accessLog.writerOutput} via caddy.service journal`),
        filtered || `Access log is configured for ${accessLog.writerOutput}, but no recent journal entries matched ${label}.`,
      );
      continue;
    }

    errors.push(`Access log writer ${accessLog.writerOutput} is configured, but lazycaddy cannot read it yet.`);
  }

  const durationMs = Math.round(performance.now() - startedAt);
  const output = chunks.filter(Boolean).join("\n").trim();

  return {
    ok: errors.length === 0 || output.length > 0,
    available: true,
    sourceLabel: label,
    output: output || errors.join("\n"),
    error: errors.length > 0 ? errors.join("\n") : undefined,
    fetchedAt,
    durationMs,
    details,
  };
}

async function tailFile(path: string, lines: number): Promise<{ ok: boolean; output: string; error?: string }> {
  try {
    const process = Bun.spawn(["tail", "-n", String(lines), path], {
      stdout: "pipe",
      stderr: "pipe",
    });
    const [stdout, stderr, exitCode] = await Promise.all([
      new Response(process.stdout).text(),
      new Response(process.stderr).text(),
      process.exited,
    ]);

    if (exitCode !== 0) {
      return { ok: false, output: stdout.trim(), error: stderr.trim() || `tail exited with code ${exitCode}` };
    }

    return { ok: true, output: stdout.trim() || `No recent entries in ${path}.` };
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return { ok: false, output: "", error: `Could not read ${path}: ${message}` };
  }
}

function filterServiceLogsForSource(serviceLogOutput: string, source: CaddySource, accessLog: CaddySource["accessLogs"][number]): string {
  const needles = [
    ...source.hosts,
    accessLog.loggerId,
    accessLog.loggerName !== "default" ? accessLog.loggerName : undefined,
  ].filter((needle): needle is string => Boolean(needle));

  if (needles.length === 0) return serviceLogOutput;

  return serviceLogOutput
    .split(/\r?\n/)
    .filter((line) => needles.some((needle) => line.includes(needle)))
    .join("\n");
}

function header(label: string): string {
  return `--- ${label} ---`;
}

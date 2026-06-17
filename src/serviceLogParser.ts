export type ParsedServiceLogEntry = {
  raw: string;
  parsed: boolean;
  timestamp?: Date;
  unit?: string;
  pid?: string;
  level?: string;
  logger?: string;
  message?: string;
  error?: string;
  file?: string;
  line?: number;
};

export function parseServiceLogOutput(output: string): ParsedServiceLogEntry[] {
  return output
    .split(/\r?\n/)
    .map(parseServiceLogLine)
    .filter((entry) => entry.raw.trim() || entry.message);
}

export function formatServiceLogOutput(output: string, errorOnly = false): string {
  const entries = parseServiceLogOutput(output);
  const visibleEntries = entries.filter((entry) => !errorOnly || isImportantServiceLogEntry(entry));

  if (visibleEntries.length === 0) {
    return errorOnly ? "No error/warning service log entries found." : "No service log entries.";
  }

  const hasParsed = visibleEntries.some((entry) => entry.parsed);
  if (!hasParsed) return visibleEntries.map((entry) => entry.raw).join("\n");

  return [serviceLogListHeader(), ...visibleEntries.map(formatServiceLogEntry)].join("\n");
}

export function parseServiceLogLine(line: string): ParsedServiceLogEntry {
  const raw = line;
  const journal = parseJournalPrefix(line);
  const messageText = journal.message || line;
  const json = parseEmbeddedJson(messageText);

  if (json) {
    return {
      raw,
      parsed: true,
      timestamp: timestampValue(json.ts) || journal.timestamp,
      unit: journal.unit,
      pid: journal.pid,
      level: stringValue(json.level),
      logger: stringValue(json.logger),
      message: stringValue(json.msg) || stringValue(json.message),
      error: stringValue(json.error) || stringValue(json.err),
      file: stringValue(json.file),
      line: numberValue(json.line),
    };
  }

  const plain = parsePlainCaddyMessage(messageText);

  return {
    raw,
    parsed: Boolean(journal.timestamp || plain.level || journal.unit),
    timestamp: journal.timestamp,
    unit: journal.unit,
    pid: journal.pid,
    level: plain.level,
    logger: plain.logger,
    message: plain.message || messageText,
    error: plain.error,
  };
}

function formatServiceLogEntry(entry: ParsedServiceLogEntry): string {
  if (!entry.parsed) return entry.raw;

  const kind = serviceLogKind(entry).padEnd(4).slice(0, 4);
  const time = entry.timestamp ? formatTime(entry.timestamp).padEnd(8) : "-".padEnd(8);
  const logger = truncateMiddle(entry.logger || entry.unit || "caddy", 22).padEnd(22);
  const message = truncateMiddle(entry.message || entry.error || entry.raw, 78);
  const suffix = entry.error ? `  ${entry.error}` : entry.file ? `  ${entry.file}${entry.line ? `:${entry.line}` : ""}` : "";

  return `${kind} ${time} ${logger} ${message}${suffix}`;
}

function serviceLogListHeader(): string {
  return "TYPE TIME     LOGGER                 MESSAGE";
}

export function isImportantServiceLogEntry(entry: ParsedServiceLogEntry): boolean {
  const kind = serviceLogKind(entry);
  if (kind === "ERR" || kind === "WARN") return true;

  return /\b(error|err|warn|warning|failed|failure|panic|fatal|unhealthy|timeout|refused)\b/i.test(entry.raw);
}

export function serviceLogKind(entry: ParsedServiceLogEntry): "OK" | "WARN" | "ERR" | "LOG" {
  if (entry.error || /^(error|fatal|panic)$/i.test(entry.level || "")) return "ERR";
  if (/^warn/i.test(entry.level || "")) return "WARN";
  if (/^info$/i.test(entry.level || "")) return "OK";

  return "LOG";
}

function parseJournalPrefix(line: string): {
  timestamp?: Date;
  unit?: string;
  pid?: string;
  message?: string;
} {
  const shortIsoMatch = line.match(/^(\S+)\s+\S+\s+([^\s:]+?)(?:\[(\d+)\])?:\s?(.*)$/);
  if (shortIsoMatch && timestampValue(shortIsoMatch[1])) {
    return {
      timestamp: timestampValue(shortIsoMatch[1]),
      unit: shortIsoMatch[2],
      pid: shortIsoMatch[3],
      message: shortIsoMatch[4] || "",
    };
  }

  const spacedTimestampMatch = line.match(/^(\S+\s+\S+)\s+\S+\s+([^\s:]+?)(?:\[(\d+)\])?:\s?(.*)$/);
  if (!spacedTimestampMatch) return { message: line };

  return {
    timestamp: timestampValue(spacedTimestampMatch[1]),
    unit: spacedTimestampMatch[2],
    pid: spacedTimestampMatch[3],
    message: spacedTimestampMatch[4] || "",
  };
}

function parsePlainCaddyMessage(message: string): {
  level?: string;
  logger?: string;
  message?: string;
  error?: string;
} {
  const parts = message.trim().split(/\s+/);
  const levelIndex = parts.findIndex((part) => /^(DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)$/i.test(part));
  if (levelIndex === -1) return { message };

  const level = parts[levelIndex]?.toLowerCase();
  const logger = parts[levelIndex + 1]?.includes("=") ? undefined : parts[levelIndex + 1];
  const rest = parts.slice(logger ? levelIndex + 2 : levelIndex + 1).join(" ");
  const errorMatch = rest.match(/(?:error|err)=([^\s]+)/i);

  return {
    level,
    logger,
    message: rest || undefined,
    error: errorMatch?.[1],
  };
}

function parseEmbeddedJson(message: string): Record<string, unknown> | undefined {
  const start = message.indexOf("{");
  if (start === -1) return undefined;

  for (let end = message.length; end > start; end--) {
    const candidate = message.slice(start, end).trim();
    if (!candidate.endsWith("}")) continue;

    try {
      const parsed = JSON.parse(candidate) as unknown;
      return isObject(parsed) ? parsed : undefined;
    } catch {
      // Try a smaller JSON suffix.
    }
  }

  return undefined;
}

function timestampValue(value: unknown): Date | undefined {
  if (typeof value === "number") {
    const millis = value > 1_000_000_000_000 ? value : value * 1000;
    const date = new Date(millis);
    return Number.isNaN(date.getTime()) ? undefined : date;
  }

  if (typeof value === "string") {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? undefined : date;
  }

  return undefined;
}

function numberValue(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }

  return undefined;
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function formatTime(date: Date): string {
  return date.toLocaleTimeString(undefined, { hour12: false });
}

function truncateMiddle(value: string, maxLength: number): string {
  if (value.length <= maxLength) return value;
  if (maxLength <= 1) return "…";

  const head = Math.ceil((maxLength - 1) / 2);
  const tail = Math.floor((maxLength - 1) / 2);

  return `${value.slice(0, head)}…${value.slice(value.length - tail)}`;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

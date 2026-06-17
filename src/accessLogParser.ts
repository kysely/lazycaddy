export type ParsedAccessLogEntry = {
  raw: string;
  parsed: boolean;
  timestamp?: Date;
  level?: string;
  logger?: string;
  message?: string;
  method?: string;
  host?: string;
  uri?: string;
  status?: number;
  durationMs?: number;
  sizeBytes?: number;
  remoteIp?: string;
  remotePort?: string;
  clientIp?: string;
  protocol?: string;
  scheme?: string;
  userAgent?: string;
  referer?: string;
  requestHeaders?: Record<string, string[]>;
  responseHeaders?: Record<string, string[]>;
  tls?: {
    version?: string;
    cipherSuite?: string;
    serverName?: string;
    protocol?: string;
  };
  error?: string;
};

export type AccessLogDetailOptions = {
  matchedRouteLines?: string[];
};

export function parseAccessLogOutput(output: string): ParsedAccessLogEntry[] {
  return output
    .split(/\r?\n/)
    .map(parseAccessLogLine)
    .filter((entry) => entry.raw.trim() || entry.message);
}

export function formatAccessLogOutput(output: string, errorOnly = false): string {
  const entries = parseAccessLogOutput(output);
  const parsedEntries = entries.filter((entry) => entry.parsed);

  if (parsedEntries.length === 0) {
    const raw = entries.map((entry) => entry.message || entry.raw).join("\n").trim();
    if (!errorOnly) return raw || "No access log entries.";

    const importantRaw = raw.split(/\r?\n/).filter(isImportantRawLine).join("\n");
    return importantRaw || raw || "No error/warning access log entries found.";
  }

  const visibleEntries = entries.filter((entry) => !errorOnly || isImportantAccessLogEntry(entry));
  const formatted = visibleEntries.map((entry) => (entry.parsed ? formatAccessLogEntry(entry) : entry.message || entry.raw));

  return formatted.length > 0
    ? [accessLogListHeader(), ...formatted].join("\n")
    : "No error/warning access log entries found.";
}

export function pickAccessLogDetailEntry(output: string, errorOnly = false): ParsedAccessLogEntry | undefined {
  const parsedEntries = parseAccessLogOutput(output).filter((entry) => entry.parsed);
  const candidates = errorOnly ? parsedEntries.filter(isImportantAccessLogEntry) : parsedEntries;

  return candidates.at(-1);
}

export function formatAccessLogDetail(
  entry: ParsedAccessLogEntry | undefined,
  options: AccessLogDetailOptions = {},
): string {
  if (!entry) return "No parsed access log entry selected.";

  const requestHeaders = formatHeaderLines(entry.requestHeaders, [
    "Referer",
    "X-Forwarded-For",
    "X-Real-IP",
    "CF-Connecting-IP",
    "Accept",
    "Content-Type",
    "Content-Length",
  ]);
  const responseHeaders = formatHeaderLines(entry.responseHeaders, [
    "Content-Type",
    "Content-Length",
    "Location",
    "Server",
    "Cache-Control",
  ]);
  const tls = compactLines([
    field("Version", entry.tls?.version),
    field("Cipher", entry.tls?.cipherSuite),
    field("SNI", entry.tls?.serverName),
    field("ALPN", entry.tls?.protocol),
  ]);
  const request = compactLines([
    field("Method", entry.method),
    field("URI", entry.uri),
    field("Host", entry.host),
    field("Scheme", entry.scheme),
    field("Remote", remoteLabel(entry)),
    field("Client IP", entry.clientIp),
    field("Protocol", entry.protocol),
    field("User-Agent", entry.userAgent),
    field("Referer", entry.referer),
  ]);
  const response = compactLines([
    field("Status", entry.status === undefined ? undefined : `${statusKind(entry.status)} ${entry.status} ${statusReason(entry.status)}`),
    field("Meaning", statusMeaning(entry.status)),
    field("Duration", durationWithClass(entry.durationMs)),
    field("Size", formatBytes(entry.sizeBytes)),
  ]);
  const metadata = compactLines([
    field("Time", entry.timestamp ? formatTimestamp(entry.timestamp) : undefined),
    field("Logger", entry.logger),
    field("Level", entry.level),
    field("Message", entry.message),
  ]);
  const error = compactLines([field("Error", entry.error)]);

  return [
    ...(options.matchedRouteLines?.length ? [...detailSection("Matched route", options.matchedRouteLines), ""] : []),
    ...detailSection("Request", request),
    ...(requestHeaders.length > 0 ? ["", ...detailSection("Request headers", requestHeaders)] : []),
    ...(tls.length > 0 ? ["", ...detailSection("TLS", tls)] : []),
    "",
    ...detailSection("Response", response),
    ...(responseHeaders.length > 0 ? ["", ...detailSection("Response headers", responseHeaders)] : []),
    ...(metadata.length > 0 ? ["", ...detailSection("Metadata", metadata)] : []),
    ...(error.length > 0 ? ["", ...detailSection("Error", error)] : []),
  ].join("\n");
}

export function parseAccessLogLine(line: string): ParsedAccessLogEntry {
  const trimmed = line.trim();
  if (!trimmed) return { raw: line, parsed: false };

  if (trimmed.startsWith("---") && trimmed.endsWith("---")) {
    return { raw: line, parsed: false, message: trimmed };
  }

  return parseJsonAccessLog(trimmed) || parseConsoleAccessLog(trimmed) || { raw: line, parsed: false };
}

export function formatAccessLogLine(line: string): string {
  const entry = parseAccessLogLine(line);
  return entry.parsed ? formatAccessLogEntry(entry) : entry.message || entry.raw;
}

export function formatAccessLogEntry(entry: ParsedAccessLogEntry): string {
  const kind = statusKind(entry.status, entry.error, entry.level).padEnd(4).slice(0, 4);
  const status = formatStatus(entry.status);
  const method = (entry.method || "-").padEnd(6).slice(0, 6);
  const uri = truncateMiddle(entry.uri || entry.message || "-", 38).padEnd(38);
  const duration = formatDuration(entry.durationMs).padStart(8);
  const size = formatBytes(entry.sizeBytes).padStart(8);
  const remote = truncateMiddle(entry.remoteIp || "-", 16).padEnd(16);
  const suffix = entry.error ? `  ${entry.error}` : "";

  return `${kind} ${status} ${method} ${uri} ${duration} ${size} ${remote}${suffix}`;
}

function parseJsonAccessLog(line: string): ParsedAccessLogEntry | undefined {
  try {
    const value = JSON.parse(line) as unknown;
    if (!isObject(value)) return undefined;

    return entryFromObject(value, line);
  } catch {
    return undefined;
  }
}

function parseConsoleAccessLog(line: string): ParsedAccessLogEntry | undefined {
  const prefix = parseConsolePrefix(line);
  const fields = parseConsoleFields(line);

  if (fields.size > 0) {
    const object: Record<string, unknown> = {};
    for (const [key, value] of fields) {
      object[key] = parseConsoleValue(value);
    }

    const entry = entryFromObject(object, line);
    applyConsolePrefix(entry, prefix);
    if (entry.parsed) return entry;
  }

  const objectFromJsonSuffix = parseEmbeddedJsonObject(line);

  if (objectFromJsonSuffix) {
    const entry = entryFromObject(objectFromJsonSuffix, line);
    applyConsolePrefix(entry, prefix);
    return entry.parsed ? entry : undefined;
  }

  return undefined;
}

function entryFromObject(value: Record<string, unknown>, raw: string): ParsedAccessLogEntry {
  const request = isObject(value.request) ? value.request : {};
  const requestHeaders = normalizeHeaders(request.headers);
  const response = isObject(value.response) ? value.response : {};
  const responseHeaders = normalizeHeaders(value.resp_headers) || normalizeHeaders(value.response_headers) || normalizeHeaders(response.headers);
  const tls = isObject(request.tls) ? request.tls : {};
  const durationSeconds = numberValue(value.duration);
  const durationMs = numberValue(value.duration_ms) ?? (durationSeconds === undefined ? undefined : durationSeconds * 1000);
  const status = numberValue(value.status);
  const size = numberValue(value.size) ?? numberValue(value.resp_headers_size) ?? numberValue(value.bytes_written);
  const message = stringValue(value.msg) || stringValue(value.message);
  const method = stringValue(request.method) || stringValue(value.method);
  const uri = stringValue(request.uri) || stringValue(value.uri) || stringValue(value.path);

  return {
    raw,
    parsed: Boolean(status || method || uri || value.request),
    timestamp: timestampValue(value.ts) || timestampValue(value.time) || timestampValue(value.timestamp),
    level: stringValue(value.level),
    logger: stringValue(value.logger),
    message,
    method,
    host: stringValue(request.host) || stringValue(value.host),
    uri,
    status,
    durationMs,
    sizeBytes: size,
    remoteIp: stringValue(request.remote_ip) || stringValue(value.remote_ip) || stringValue(value.remote) || stringValue(value.client_ip),
    remotePort: stringValue(request.remote_port) || stringValue(value.remote_port),
    clientIp: stringValue(request.client_ip) || stringValue(value.client_ip),
    protocol: stringValue(request.proto) || stringValue(value.proto),
    scheme: stringValue(request.scheme) || stringValue(value.scheme),
    userAgent: headerValue(requestHeaders, "User-Agent") || stringValue(value.user_agent),
    referer: headerValue(requestHeaders, "Referer") || stringValue(value.referer),
    requestHeaders,
    responseHeaders,
    tls: {
      version: stringValue(tls.version),
      cipherSuite: stringValue(tls.cipher_suite) || stringValue(tls.cipherSuite),
      serverName: stringValue(tls.server_name) || stringValue(tls.serverName),
      protocol: stringValue(tls.proto) || stringValue(tls.protocol),
    },
    error: stringValue(value.error) || stringValue(value.err),
  };
}

function applyConsolePrefix(
  entry: ParsedAccessLogEntry,
  prefix: { timestamp?: Date; level?: string; logger?: string; message?: string },
): void {
  if (prefix.timestamp && !entry.timestamp) entry.timestamp = prefix.timestamp;
  if (prefix.level && !entry.level) entry.level = prefix.level;
  if (prefix.logger && !entry.logger) entry.logger = prefix.logger;
  if (prefix.message && !entry.message) entry.message = prefix.message;
}

function parseEmbeddedJsonObject(line: string): Record<string, unknown> | undefined {
  const start = line.indexOf("{");
  if (start === -1) return undefined;

  for (let end = line.length; end > start; end--) {
    const candidate = line.slice(start, end).trim();
    if (!candidate.endsWith("}")) continue;

    try {
      const parsed = JSON.parse(candidate) as unknown;
      return isObject(parsed) ? parsed : undefined;
    } catch {
      // Try a shorter suffix. Console encoders usually put JSON at the end, but
      // some shells/loggers append punctuation.
    }
  }

  return undefined;
}

function parseConsoleFields(line: string): Map<string, string> {
  const fields = new Map<string, string>();
  const keyRegex = /(?:^|\s)([A-Za-z_][\w.-]*)=/g;
  const matches = [...line.matchAll(keyRegex)];

  for (let index = 0; index < matches.length; index++) {
    const match = matches[index];
    const key = match?.[1];
    if (!key || match.index === undefined) continue;

    const valueStart = match.index + match[0].length;
    const nextMatch = matches[index + 1];
    const valueEnd = nextMatch?.index ?? line.length;
    const rawValue = line.slice(valueStart, valueEnd).trim();

    fields.set(key, stripTrailingConsolePunctuation(rawValue));
  }

  return fields;
}

function parseConsoleValue(value: string): unknown {
  const trimmed = value.trim();
  if (!trimmed) return "";

  if ((trimmed.startsWith("{") && trimmed.endsWith("}")) || (trimmed.startsWith("[") && trimmed.endsWith("]"))) {
    try {
      return JSON.parse(trimmed);
    } catch {
      return trimmed;
    }
  }

  if ((trimmed.startsWith('"') && trimmed.endsWith('"')) || (trimmed.startsWith("'") && trimmed.endsWith("'"))) {
    try {
      return JSON.parse(trimmed);
    } catch {
      return trimmed.slice(1, -1);
    }
  }

  const numeric = Number(trimmed);
  if (Number.isFinite(numeric)) return numeric;

  return trimmed;
}

function parseConsolePrefix(line: string): { timestamp?: Date; level?: string; logger?: string; message?: string } {
  const parts = line.trim().split(/\s+/);
  let timestamp: Date | undefined;
  let consumedTimestampParts = 0;

  if (parts[0] && parts[1]) {
    timestamp = timestampValue(`${parts[0]} ${parts[1]}`) || timestampValue(parts[0]);
    consumedTimestampParts = timestampValue(`${parts[0]} ${parts[1]}`) ? 2 : timestamp ? 1 : 0;
  } else if (parts[0]) {
    timestamp = timestampValue(parts[0]);
    consumedTimestampParts = timestamp ? 1 : 0;
  }

  const levelIndex = parts.findIndex((part, index) =>
    index >= consumedTimestampParts && /^(DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)$/i.test(part),
  );
  const level = levelIndex >= 0 ? parts[levelIndex]?.toLowerCase() : undefined;
  const logger = levelIndex >= 0 ? parts[levelIndex + 1] : undefined;
  const message = levelIndex >= 0 ? parts.slice(levelIndex + 2).find((part) => !part.includes("=") && !part.startsWith("{")) : undefined;

  return { timestamp, level, logger, message };
}

export function accessLogListHeader(): string {
  return "TYPE CODE METHOD URI                                      TIME     SIZE REMOTE";
}

function detailSection(title: string, lines: string[]): string[] {
  if (lines.length === 0) return [];

  return [title, ...lines.map((line) => `  ${line}`)];
}

function field(name: string, value: string | undefined): string | undefined {
  return value ? `${name}: ${value}` : undefined;
}

function compactLines(lines: Array<string | undefined>): string[] {
  return lines.filter((line): line is string => Boolean(line));
}

function formatHeaderLines(headers: Record<string, string[]> | undefined, names: string[]): string[] {
  if (!headers) return [];

  return names
    .map((name) => field(name, headerValue(headers, name)))
    .filter((line): line is string => Boolean(line));
}

function remoteLabel(entry: ParsedAccessLogEntry): string | undefined {
  if (!entry.remoteIp) return undefined;

  return entry.remotePort ? `${entry.remoteIp}:${entry.remotePort}` : entry.remoteIp;
}

function durationWithClass(durationMs: number | undefined): string | undefined {
  const formatted = formatDuration(durationMs);
  if (formatted === "-") return undefined;

  return `${formatted} (${latencyClass(durationMs)})`;
}

function latencyClass(durationMs: number | undefined): string {
  if (durationMs === undefined) return "unknown";
  if (durationMs < 100) return "fast";
  if (durationMs < 500) return "ok";
  if (durationMs < 2000) return "slow";

  return "very slow";
}

function statusReason(status: number): string {
  const reasons: Record<number, string> = {
    200: "OK",
    201: "Created",
    204: "No Content",
    301: "Moved Permanently",
    302: "Found",
    304: "Not Modified",
    400: "Bad Request",
    401: "Unauthorized",
    403: "Forbidden",
    404: "Not Found",
    408: "Request Timeout",
    429: "Too Many Requests",
    500: "Internal Server Error",
    502: "Bad Gateway",
    503: "Service Unavailable",
    504: "Gateway Timeout",
  };

  return reasons[status] || "";
}

function statusMeaning(status: number | undefined): string | undefined {
  if (status === undefined) return undefined;
  if (status >= 200 && status < 300) return "successful response";
  if (status >= 300 && status < 400) return "redirect/cache response";
  if (status === 401) return "authentication required";
  if (status === 403) return "forbidden by config or upstream";
  if (status === 404) return "route/upstream returned not found";
  if (status === 502) return "upstream/proxy failure";
  if (status === 503) return "service unavailable";
  if (status === 504) return "upstream timeout";
  if (status >= 400 && status < 500) return "client error";
  if (status >= 500) return "server/upstream error";

  return undefined;
}

function normalizeHeaders(value: unknown): Record<string, string[]> | undefined {
  if (!isObject(value)) return undefined;

  const headers: Record<string, string[]> = {};
  for (const [name, rawValue] of Object.entries(value)) {
    if (Array.isArray(rawValue)) {
      const values = rawValue.map(String).filter(Boolean);
      if (values.length > 0) headers[name] = values;
      continue;
    }

    if (rawValue !== undefined && rawValue !== null) {
      headers[name] = [String(rawValue)];
    }
  }

  return Object.keys(headers).length > 0 ? headers : undefined;
}

function stripTrailingConsolePunctuation(value: string): string {
  return value.replace(/,$/, "");
}

function statusKind(status?: number, error?: string, level?: string): "OK" | "WARN" | "ERR" | "LOG" {
  if (error || /^(error|fatal|panic)$/i.test(level || "")) return "ERR";
  if (/^warn/i.test(level || "")) return "WARN";
  if (status === undefined) return "LOG";
  if (status >= 500) return "ERR";
  if (status >= 400) return "WARN";

  return "OK";
}

function formatStatus(status: number | undefined): string {
  if (status === undefined) return "---";

  return String(status).padStart(3);
}

function formatDuration(durationMs: number | undefined): string {
  if (durationMs === undefined || !Number.isFinite(durationMs)) return "-";
  if (durationMs < 1) return `${durationMs.toFixed(1)}ms`;
  if (durationMs < 1000) return `${Math.round(durationMs)}ms`;

  return `${(durationMs / 1000).toFixed(2)}s`;
}

function formatBytes(bytes: number | undefined): string {
  if (bytes === undefined || !Number.isFinite(bytes)) return "-";
  if (bytes < 1024) return `${bytes}B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;

  return `${(bytes / 1024 / 1024).toFixed(1)}MB`;
}

function formatTimestamp(date: Date): string {
  return date.toISOString().replace("T", " ").replace("Z", "");
}

export function isImportantAccessLogEntry(entry: ParsedAccessLogEntry): boolean {
  if (entry.parsed) {
    const kind = statusKind(entry.status, entry.error, entry.level);
    if (kind === "WARN" || kind === "ERR") return true;
  }

  return isImportantRawLine(entry.raw);
}

function isImportantRawLine(line: string): boolean {
  return /\b(error|err|warn|warning|failed|failure|panic|fatal|unhealthy|timeout|refused)\b/i.test(line)
    || /\bstatus[=: ]+5\d\d\b/i.test(line)
    || /\s[45]\d\d\s/.test(line);
}

function truncateMiddle(value: string, maxLength: number): string {
  if (value.length <= maxLength) return value;
  if (maxLength <= 1) return "…";

  const head = Math.ceil((maxLength - 1) / 2);
  const tail = Math.floor((maxLength - 1) / 2);

  return `${value.slice(0, head)}…${value.slice(value.length - tail)}`;
}

function timestampValue(value: unknown): Date | undefined {
  if (typeof value === "number") {
    const millis = value > 1_000_000_000_000 ? value : value * 1000;
    const date = new Date(millis);
    return Number.isNaN(date.getTime()) ? undefined : date;
  }

  if (typeof value === "string") {
    const normalized = value.includes("/") ? value.replace(/^([^ ]+) /, (match) => match.replace(/\//g, "-")) : value;
    const date = new Date(normalized);
    return Number.isNaN(date.getTime()) ? undefined : date;
  }

  return undefined;
}

function numberValue(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string") {
    const number = Number(value);
    if (Number.isFinite(number)) return number;
  }

  return undefined;
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function headerValue(headers: Record<string, string[]> | undefined, name: string): string | undefined {
  if (!headers) return undefined;

  const exactKey = Object.keys(headers).find((key) => key.toLowerCase() === name.toLowerCase());
  const value = exactKey ? headers[exactKey] : undefined;

  return value?.[0];
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

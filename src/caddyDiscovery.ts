import { readFile } from "node:fs/promises";
import { basename } from "node:path";
import {
  DEFAULT_ADMIN_API_URL,
  getExplicitAdminApiBaseUrl,
  normalizeAdminUrl,
  type ExplicitAdminApiBaseUrl,
} from "./caddyApi.ts";

type AdminConfigDiscovery = {
  adminUrl?: string;
  disabled?: boolean;
  listen?: string;
  source: string;
};

type ParsedCaddyRunCommand = {
  argv: string[];
  configPath?: string;
  adapter?: string;
  resume?: boolean;
};

type SystemdServiceInfo = {
  exists: boolean;
  loadState?: string;
  activeState?: string;
  subState?: string;
  mainPid?: number;
  fragmentPath?: string;
  execStart?: string;
  argv?: string[];
  argvSource?: "proc" | "systemd";
  error?: string;
};

export type AdminApiDiscovery = {
  adminUrl?: string;
  source:
    | "cli"
    | "env"
    | "systemd-config"
    | "systemd-default"
    | "process-config"
    | "process-default"
    | "default"
    | "disabled"
    | "unsupported";
  sourceLabel: string;
  service?: SystemdServiceInfo;
  command?: ParsedCaddyRunCommand;
  configPath?: string;
  adapter?: string;
  disabled?: boolean;
  notes: string[];
};

export async function discoverAdminApiEndpoint(
  argv: string[] = Bun.argv,
  env: NodeJS.ProcessEnv = process.env,
): Promise<AdminApiDiscovery> {
  const notes: string[] = [];
  const explicit = getExplicitAdminApiBaseUrl(argv, env);
  const service = await inspectSystemdCaddyService();

  if (explicit) {
    const explicitResult = explicitDiscovery(explicit);
    const serviceCommand = service ? parseCaddyRunCommand(service.argv || []) : undefined;

    return {
      ...explicitResult,
      service,
      command: serviceCommand,
      configPath: serviceCommand?.configPath,
      adapter: serviceCommand?.adapter,
      notes: [
        ...explicitResult.notes,
        ...(service ? [formatServiceNote(service)] : ["systemd caddy.service was not found or could not be inspected."]),
      ],
    };
  }

  if (service) {
    notes.push(formatServiceNote(service));

    const serviceCommand = parseCaddyRunCommand(service.argv || []);
    if (serviceCommand) {
      const discovered = await discoverFromCommand(serviceCommand, notes);
      if (discovered) {
        return {
          ...discovered,
          service,
          command: serviceCommand,
        };
      }
    } else if (service.exists) {
      notes.push("Could not parse a caddy run command from systemd's caddy.service.");
    }

    if (service.exists) {
      notes.push("No admin endpoint was found in the service command/config; assuming Caddy's default admin endpoint.");
      return {
        adminUrl: DEFAULT_ADMIN_API_URL,
        source: "systemd-default",
        sourceLabel: "systemd caddy.service default",
        service,
        notes,
      };
    }
  }

  const processCommand = await inspectCaddyProcess();
  if (processCommand) {
    notes.push("Found a running caddy process with pgrep.");
    const discovered = await discoverFromCommand(processCommand, notes, "process");
    if (discovered) return { ...discovered, command: processCommand };

    notes.push("No admin endpoint was found in the process command/config; assuming Caddy's default admin endpoint.");
    return {
      adminUrl: DEFAULT_ADMIN_API_URL,
      source: "process-default",
      sourceLabel: "caddy process default",
      command: processCommand,
      notes,
    };
  }

  notes.push("No explicit endpoint, systemd caddy.service, or caddy process was discovered; using Caddy's default endpoint.");
  return {
    adminUrl: DEFAULT_ADMIN_API_URL,
    source: "default",
    sourceLabel: "Caddy default",
    notes,
  };
}

export function discoverySummary(discovery: AdminApiDiscovery): string {
  if (discovery.disabled) return `Admin API disabled by ${discovery.sourceLabel}`;

  return `${discovery.adminUrl || "not discovered"} (${discovery.sourceLabel})`;
}

function explicitDiscovery(explicit: ExplicitAdminApiBaseUrl): AdminApiDiscovery {
  return {
    adminUrl: explicit.url,
    source: explicit.source,
    sourceLabel: explicit.source === "cli" ? "CLI override" : "environment override",
    notes: [
      explicit.source === "cli"
        ? "Using --admin-url/--admin from the lazycaddy command line."
        : "Using CADDY_ADMIN_API/CADDY_ADMIN_URL from the environment.",
    ],
  };
}

async function discoverFromCommand(
  command: ParsedCaddyRunCommand,
  notes: string[],
  origin: "systemd" | "process" = "systemd",
): Promise<AdminApiDiscovery | undefined> {
  if (command.configPath) {
    notes.push(`Caddy command uses config: ${command.configPath}${command.adapter ? ` (${command.adapter})` : ""}.`);

    const configDiscovery = await discoverFromConfig(command.configPath, command.adapter);
    notes.push(configDiscovery.source);

    if (configDiscovery.disabled) {
      return {
        source: "disabled",
        sourceLabel: `${origin} config ${command.configPath}`,
        configPath: command.configPath,
        adapter: command.adapter,
        disabled: true,
        notes,
      };
    }

    if (configDiscovery.adminUrl) {
      return {
        adminUrl: configDiscovery.adminUrl,
        source: origin === "systemd" ? "systemd-config" : "process-config",
        sourceLabel: `${origin} config ${command.configPath}`,
        configPath: command.configPath,
        adapter: command.adapter,
        notes,
      };
    }

    if (configDiscovery.listen) {
      return {
        source: "unsupported",
        sourceLabel: `${origin} config ${command.configPath}`,
        configPath: command.configPath,
        adapter: command.adapter,
        notes,
      };
    }
  }

  if (command.resume) {
    notes.push("Caddy was started with --resume; the resumed config cannot be inspected before reaching the Admin API.");
  }

  return undefined;
}

async function discoverFromConfig(configPath: string, adapter?: string): Promise<AdminConfigDiscovery> {
  try {
    const content = await readFile(configPath, "utf8");
    const format = inferConfigFormat(configPath, adapter);

    if (format === "json") {
      return discoverFromJsonConfig(content);
    }

    return discoverFromCaddyfile(content);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return {
      source: `Could not read ${configPath}: ${message}. Falling back to default endpoint.`,
      adminUrl: DEFAULT_ADMIN_API_URL,
    };
  }
}

function discoverFromJsonConfig(content: string): AdminConfigDiscovery {
  try {
    const config = JSON.parse(content) as unknown;
    if (!isObject(config)) {
      return { source: "JSON config is not an object. Falling back to default endpoint.", adminUrl: DEFAULT_ADMIN_API_URL };
    }

    const admin = config.admin;
    if (!isObject(admin)) {
      return { source: "JSON config has no admin block; using Caddy's default endpoint.", adminUrl: DEFAULT_ADMIN_API_URL };
    }

    if (admin.disabled === true) {
      return { source: "JSON config sets admin.disabled=true.", disabled: true };
    }

    if (typeof admin.listen === "string") {
      return listenToAdminUrl(admin.listen, "JSON config admin.listen");
    }

    return { source: "JSON config admin block has no listen address; using Caddy's default endpoint.", adminUrl: DEFAULT_ADMIN_API_URL };
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return { source: `Could not parse JSON config: ${message}. Falling back to default endpoint.`, adminUrl: DEFAULT_ADMIN_API_URL };
  }
}

function discoverFromCaddyfile(content: string): AdminConfigDiscovery {
  const globalBlock = extractCaddyfileGlobalBlock(content);

  if (!globalBlock) {
    return { source: "Caddyfile has no global options block; using Caddy's default endpoint.", adminUrl: DEFAULT_ADMIN_API_URL };
  }

  for (const rawLine of globalBlock) {
    const line = stripComment(rawLine).trim();
    if (!line || line === "{" || line === "}") continue;

    const tokens = line.split(/\s+/);
    if (tokens[0] !== "admin") continue;

    const value = tokens[1];
    if (value === "off") {
      return { source: "Caddyfile global options set `admin off`.", disabled: true };
    }

    if (value && value !== "{") {
      return listenToAdminUrl(value, "Caddyfile global admin option");
    }

    return { source: "Caddyfile global admin option has no explicit address; using Caddy's default endpoint.", adminUrl: DEFAULT_ADMIN_API_URL };
  }

  return { source: "Caddyfile global options do not override admin; using Caddy's default endpoint.", adminUrl: DEFAULT_ADMIN_API_URL };
}

async function inspectSystemdCaddyService(): Promise<SystemdServiceInfo | undefined> {
  const result = await runCommand([
    "systemctl",
    "show",
    "caddy",
    "--no-pager",
    "--property=LoadState",
    "--property=ActiveState",
    "--property=SubState",
    "--property=MainPID",
    "--property=FragmentPath",
    "--property=ExecStart",
  ]);

  if (!result.ok && !result.stdout) return undefined;

  const properties = parseProperties(result.stdout);
  const loadState = properties.LoadState;
  const exists = Boolean(loadState && loadState !== "not-found");
  const mainPid = numberOrUndefined(properties.MainPID);
  const procArgv = mainPid && mainPid > 0 ? await readProcCmdline(mainPid) : undefined;
  const systemdArgv = extractSystemdArgv(properties.ExecStart || "");

  return {
    exists,
    loadState,
    activeState: properties.ActiveState,
    subState: properties.SubState,
    mainPid,
    fragmentPath: properties.FragmentPath,
    execStart: properties.ExecStart,
    argv: procArgv || systemdArgv,
    argvSource: procArgv ? "proc" : systemdArgv ? "systemd" : undefined,
    error: result.ok ? undefined : result.stderr || result.stdout,
  };
}

async function inspectCaddyProcess(): Promise<ParsedCaddyRunCommand | undefined> {
  const result = await runCommand(["pgrep", "-a", "caddy"]);
  if (!result.ok || !result.stdout.trim()) return undefined;

  for (const line of result.stdout.trim().split(/\r?\n/)) {
    const commandLine = line.replace(/^\d+\s+/, "");
    const argv = splitShellWords(commandLine);
    const command = parseCaddyRunCommand(argv);
    if (command) return command;
  }

  return undefined;
}

function parseCaddyRunCommand(argv: string[]): ParsedCaddyRunCommand | undefined {
  if (argv.length === 0) return undefined;

  const executable = basename(argv[0] || "");
  if (executable !== "caddy" && !executable.endsWith("/caddy")) return undefined;

  const command: ParsedCaddyRunCommand = { argv };

  for (let index = 1; index < argv.length; index++) {
    const arg = argv[index];
    if (!arg) continue;

    if (arg === "--config" || arg === "-config") {
      command.configPath = argv[index + 1];
      index++;
      continue;
    }

    if (arg.startsWith("--config=")) {
      command.configPath = arg.slice("--config=".length);
      continue;
    }

    if (arg === "--adapter" || arg === "-adapter") {
      command.adapter = argv[index + 1];
      index++;
      continue;
    }

    if (arg.startsWith("--adapter=")) {
      command.adapter = arg.slice("--adapter=".length);
      continue;
    }

    if (arg === "--resume") {
      command.resume = true;
    }
  }

  return command;
}

function listenToAdminUrl(listen: string, source: string): AdminConfigDiscovery {
  if (listen === "off") return { source: `${source} is off.`, disabled: true };

  if (listen.startsWith("unix/") || listen.startsWith("unix:")) {
    return {
      source: `${source} uses a Unix socket (${listen}); lazycaddy cannot query Unix-socket admin endpoints yet.`,
      listen,
    };
  }

  let normalizedListen = listen;
  if (normalizedListen.startsWith("tcp/")) normalizedListen = normalizedListen.slice("tcp/".length);

  if (normalizedListen.startsWith(":")) normalizedListen = `localhost${normalizedListen}`;

  return {
    source: `${source} is ${listen}.`,
    listen,
    adminUrl: normalizeAdminUrl(normalizedListen),
  };
}

function extractCaddyfileGlobalBlock(content: string): string[] | undefined {
  const lines = content.split(/\r?\n/);
  const firstMeaningfulLineIndex = lines.findIndex((line) => stripComment(line).trim().length > 0);
  if (firstMeaningfulLineIndex === -1) return undefined;

  const firstLine = stripComment(lines[firstMeaningfulLineIndex] || "").trim();
  if (firstLine !== "{") return undefined;

  const block: string[] = [];
  let depth = 0;

  for (let index = firstMeaningfulLineIndex; index < lines.length; index++) {
    const line = lines[index] || "";
    const cleanLine = stripComment(line);

    for (const char of cleanLine) {
      if (char === "{") depth++;
      if (char === "}") depth--;
    }

    block.push(line);

    if (index > firstMeaningfulLineIndex && depth <= 0) {
      return block;
    }
  }

  return block;
}

function stripComment(line: string): string {
  let quote: '"' | "'" | null = null;

  for (let index = 0; index < line.length; index++) {
    const char = line[index];
    const previous = line[index - 1];

    if ((char === '"' || char === "'") && previous !== "\\") {
      quote = quote === char ? null : quote ?? char;
    }

    if (char === "#" && !quote) {
      return line.slice(0, index);
    }
  }

  return line;
}

function inferConfigFormat(configPath: string, adapter?: string): "json" | "caddyfile" {
  if (adapter === "json") return "json";
  if (adapter === "caddyfile") return "caddyfile";
  if (configPath.endsWith(".json")) return "json";

  return "caddyfile";
}

function extractSystemdArgv(execStart: string): string[] | undefined {
  const match = execStart.match(/argv\[\]=(.*?)(?:\s;\s|$)/);
  if (!match?.[1]) return undefined;

  return splitShellWords(match[1]);
}

function splitShellWords(input: string): string[] {
  const words: string[] = [];
  let current = "";
  let quote: '"' | "'" | null = null;
  let escaping = false;

  for (const char of input.trim()) {
    if (escaping) {
      current += char;
      escaping = false;
      continue;
    }

    if (char === "\\" && quote !== "'") {
      escaping = true;
      continue;
    }

    if ((char === '"' || char === "'") && !quote) {
      quote = char;
      continue;
    }

    if (char === quote) {
      quote = null;
      continue;
    }

    if (/\s/.test(char) && !quote) {
      if (current) {
        words.push(current);
        current = "";
      }
      continue;
    }

    current += char;
  }

  if (current) words.push(current);

  return words;
}

function parseProperties(output: string): Record<string, string> {
  const properties: Record<string, string> = {};

  for (const line of output.split(/\r?\n/)) {
    const separatorIndex = line.indexOf("=");
    if (separatorIndex === -1) continue;

    properties[line.slice(0, separatorIndex)] = line.slice(separatorIndex + 1);
  }

  return properties;
}

async function readProcCmdline(pid: number): Promise<string[] | undefined> {
  try {
    const content = await readFile(`/proc/${pid}/cmdline`, "utf8");
    const argv = content.split("\0").filter(Boolean);

    return argv.length > 0 ? argv : undefined;
  } catch {
    return undefined;
  }
}

async function runCommand(args: string[], timeoutMs = 1500): Promise<{ ok: boolean; stdout: string; stderr: string; exitCode?: number }> {
  try {
    const process = Bun.spawn(args, {
      stdout: "pipe",
      stderr: "pipe",
    });

    const timeout = setTimeout(() => process.kill(), timeoutMs);
    const [stdout, stderr, exitCode] = await Promise.all([
      new Response(process.stdout).text(),
      new Response(process.stderr).text(),
      process.exited,
    ]).finally(() => clearTimeout(timeout));

    return { ok: exitCode === 0, stdout, stderr, exitCode };
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return { ok: false, stdout: "", stderr: message };
  }
}

function formatServiceNote(service: SystemdServiceInfo): string {
  if (!service.exists) return `systemd caddy.service not found (${service.loadState || "unknown"}).`;

  const state = [service.loadState, service.activeState, service.subState].filter(Boolean).join("/");
  const pid = service.mainPid ? ` pid ${service.mainPid}` : "";
  const argvSource = service.argvSource ? ` command from ${service.argvSource}` : "";

  return `Found systemd caddy.service: ${state || "unknown"}${pid}${argvSource}.`;
}

function numberOrUndefined(value: string | undefined): number | undefined {
  if (!value) return undefined;

  const number = Number(value);
  return Number.isFinite(number) ? number : undefined;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

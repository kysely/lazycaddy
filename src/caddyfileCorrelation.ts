import { readFile } from "node:fs/promises";
import { type CaddyRouteAction, type CaddyRouteRule, type CaddySource } from "./caddyConfig.ts";

export type CaddyfileDirective = {
  line: number;
  text: string;
  name: string;
  args: string[];
  matcher?: string;
  matcherLine?: number;
};

export type CaddyfileSourceBlock = {
  path: string;
  line: number;
  endLine?: number;
  address: string;
  addresses: string[];
  directives: CaddyfileDirective[];
};

export type CaddyfileCorrelation = {
  path?: string;
  available: boolean;
  error?: string;
  contentLines?: string[];
  sources: CaddyfileSourceBlock[];
};

type StackItem = {
  type: "site" | "matcher" | "other";
  source?: CaddyfileSourceBlock;
  matcher?: string;
  matcherLine?: number;
};

const routeDirectiveNames = new Set([
  "reverse_proxy",
  "file_server",
  "respond",
  "redir",
  "rewrite",
  "header",
  "headers",
  "encode",
  "php_fastcgi",
  "request_body",
  "basic_auth",
  "basicauth",
  "templates",
  "try_files",
  "uri",
  "map",
  "root",
]);

export async function loadCaddyfileCorrelation(
  configPath: string | undefined,
  adapter?: string,
): Promise<CaddyfileCorrelation> {
  if (!configPath) {
    return { available: false, error: "No config path discovered.", sources: [] };
  }

  if (adapter && adapter !== "caddyfile") {
    return {
      path: configPath,
      available: false,
      error: `Config adapter is ${adapter}; Caddyfile correlation only supports Caddyfile configs for now.`,
      sources: [],
    };
  }

  if (configPath.endsWith(".json")) {
    return {
      path: configPath,
      available: false,
      error: "Config appears to be JSON; Caddyfile correlation is not available.",
      sources: [],
    };
  }

  try {
    const content = await readFile(configPath, "utf8");
    return {
      path: configPath,
      available: true,
      contentLines: content.split(/\r?\n/),
      sources: parseCaddyfile(configPath, content),
    };
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return {
      path: configPath,
      available: false,
      error: `Could not read ${configPath}: ${message}`,
      sources: [],
    };
  }
}

export function findCaddyfileSource(
  correlation: CaddyfileCorrelation,
  source: CaddySource | undefined,
): CaddyfileSourceBlock | undefined {
  if (!source || !correlation.available) return undefined;

  const sourceHosts = source.hosts.map(normalizeAddressForHost).filter(Boolean);
  const sourceListeners = source.listen.map(normalizeAddressForListener).filter(Boolean);

  for (const block of correlation.sources) {
    const blockHosts = block.addresses.map(normalizeAddressForHost).filter(Boolean);
    if (sourceHosts.some((host) => blockHosts.includes(host))) return block;
  }

  for (const block of correlation.sources) {
    const blockListeners = block.addresses.map(normalizeAddressForListener).filter(Boolean);
    if (sourceListeners.some((listener) => blockListeners.includes(listener))) return block;
  }

  return undefined;
}

export function findCaddyfileRouteLine(
  block: CaddyfileSourceBlock | undefined,
  route: CaddyRouteRule,
): number | undefined {
  return findCaddyfileDirective(block, route)?.line;
}

export function caddyfileLocation(
  correlation: CaddyfileCorrelation,
  block: CaddyfileSourceBlock | undefined,
  line?: number,
): string | undefined {
  const path = correlation.path || block?.path;
  if (!path || !block) return undefined;

  return `${path}:${line || block.line}`;
}

export function formatCaddyfileBlock(
  correlation: CaddyfileCorrelation,
  block: CaddyfileSourceBlock | undefined,
): string[] {
  if (!block) return [correlation.error || "No correlated Caddyfile block found."];
  if (!correlation.contentLines) return ["Caddyfile contents are not available."];

  const start = Math.max(1, block.line);
  const end = Math.min(correlation.contentLines.length, block.endLine || block.line);
  const width = String(end).length;

  return correlation.contentLines
    .slice(start - 1, end)
    .map((line, index) => `${String(start + index).padStart(width, " ")} │ ${line}`);
}

function parseCaddyfile(path: string, content: string): CaddyfileSourceBlock[] {
  const sources: CaddyfileSourceBlock[] = [];
  const stack: StackItem[] = [];
  const lines = content.split(/\r?\n/);

  for (let index = 0; index < lines.length; index++) {
    const lineNumber = index + 1;
    const cleaned = stripComment(lines[index] || "").trim();
    if (!cleaned) continue;

    const closeCount = countChar(cleaned, "}");
    for (let count = 0; count < closeCount && cleaned.startsWith("}"); count++) {
      closeStack(stack, lineNumber);
    }

    const withoutLeadingClosers = cleaned.replace(/^}+\s*/, "").trim();
    if (!withoutLeadingClosers) continue;

    const opensBlock = withoutLeadingClosers.includes("{");
    const beforeBrace = opensBlock ? withoutLeadingClosers.slice(0, withoutLeadingClosers.indexOf("{")).trim() : withoutLeadingClosers;
    const tokens = splitWords(beforeBrace);
    if (tokens.length === 0) continue;

    const currentSource = findCurrentSource(stack);
    if (currentSource && isRouteDirective(tokens[0])) {
      currentSource.directives.push({
        line: lineNumber,
        text: beforeBrace,
        name: normalizeDirectiveName(tokens[0]!),
        args: tokens.slice(1),
        ...currentMatcher(stack),
      });
    }

    if (opensBlock) {
      if (!currentSource && isSiteBlockHeader(beforeBrace)) {
        const addresses = parseSiteAddresses(beforeBrace);
        const source: CaddyfileSourceBlock = {
          path,
          line: lineNumber,
          address: addresses.join(", "),
          addresses,
          directives: [],
        };
        sources.push(source);
        stack.push({ type: "site", source });
      } else if (currentSource) {
        const matcher = matcherFromBlockHeader(tokens);
        stack.push({
          type: matcher ? "matcher" : "other",
          source: currentSource,
          matcher: matcher?.matcher,
          matcherLine: matcher?.line ?? lineNumber,
        });
      } else {
        stack.push({ type: "other" });
      }
    }

    const nonLeadingCloseCount = cleaned.startsWith("}") ? Math.max(0, closeCount - 1) : closeCount;
    for (let count = 0; count < nonLeadingCloseCount; count++) {
      closeStack(stack, lineNumber);
    }
  }

  return sources;
}

function closeStack(stack: StackItem[], lineNumber: number): void {
  const closed = stack.pop();
  if (closed?.type === "site" && closed.source) {
    closed.source.endLine = lineNumber;
  }
}

function isSiteBlockHeader(header: string): boolean {
  if (!header || header === "{") return false;
  if (header.startsWith("(")) return false;

  const first = splitWords(header)[0];
  if (!first) return false;

  return ![
    "admin",
    "email",
    "debug",
    "log",
    "storage",
    "acme_ca",
    "auto_https",
    "servers",
    "order",
    "skip_install_trust",
  ].includes(first);
}

function parseSiteAddresses(header: string): string[] {
  return header
    .split(/[,\s]+/)
    .map((part) => part.trim())
    .filter(Boolean);
}

function matcherFromBlockHeader(tokens: string[]): { matcher: string; line?: number } | undefined {
  const [name, ...args] = tokens;
  if (!name) return undefined;

  switch (name) {
    case "handle":
      return { matcher: args.length > 0 ? matcherFromArgs(args) : "everything else" };
    case "handle_path":
      return { matcher: args.length > 0 ? matcherFromArgs(args) : "path *" };
    case "route":
      return args.length > 0 ? { matcher: matcherFromArgs(args) } : undefined;
    case "handle_errors":
      return { matcher: "handle_errors" };
    default:
      return undefined;
  }
}

function matcherFromArgs(args: string[]): string {
  if (args.length === 0) return "all requests";
  if (args[0]?.startsWith("@")) return args[0];

  return args.map((arg) => (arg.startsWith("/") ? `path ${arg}` : arg)).join(" ");
}

function findCaddyfileDirective(
  block: CaddyfileSourceBlock | undefined,
  route: CaddyRouteRule,
): CaddyfileDirective | undefined {
  if (!block) return undefined;

  const actionKinds = route.actions.map((action) => action.kind);
  const candidates = block.directives.filter((directive) =>
    actionKinds.some((kind) => directiveMatchesAction(directive, kind)),
  );
  if (candidates.length === 0) return undefined;

  return candidates
    .map((directive) => ({ directive, score: scoreDirective(directive, route) }))
    .sort((left, right) => right.score - left.score || left.directive.line - right.directive.line)[0]?.directive;
}

function scoreDirective(directive: CaddyfileDirective, route: CaddyRouteRule): number {
  let score = 0;
  const routeMatcher = route.matcher;

  if (routeMatcher === "everything else" && directive.matcher === "everything else") score += 20;
  if (routeMatcher === "all requests" && !directive.matcher) score += 20;
  if (directive.matcher && routeMatcher !== "all requests" && matcherOverlaps(routeMatcher, directive.matcher)) score += 20;

  for (const action of route.actions) {
    if (directiveMatchesAction(directive, action.kind)) score += 5;
    if (actionMentionsDirectiveArgs(action, directive)) score += 5;
  }

  return score;
}

function directiveMatchesAction(directive: CaddyfileDirective, actionKind: string): boolean {
  const directiveName = normalizeDirectiveName(directive.name);
  const normalizedAction = normalizeDirectiveName(actionKind);

  if (directiveName === normalizedAction) return true;
  if (normalizedAction === "headers" && directiveName === "header") return true;
  if (normalizedAction === "reverse_proxy" && directiveName === "php_fastcgi") return true;
  if (normalizedAction === "authentication" && directiveName === "basic_auth") return true;
  if (normalizedAction === "respond" && directiveName === "redir") return true;

  return false;
}

function actionMentionsDirectiveArgs(action: CaddyRouteAction, directive: CaddyfileDirective): boolean {
  const actionText = [action.label, ...(action.upstreams?.map((upstream) => upstream.label) || [])].join(" ");

  return directive.args.some((arg) => actionText.includes(arg));
}

function matcherOverlaps(routeMatcher: string, directiveMatcher: string): boolean {
  if (routeMatcher === directiveMatcher) return true;

  const routePaths = [...routeMatcher.matchAll(/path\s+([^;]+)/g)].map((match) => match[1]?.trim()).filter(Boolean);
  const directivePaths = [...directiveMatcher.matchAll(/path\s+([^;]+)/g)].map((match) => match[1]?.trim()).filter(Boolean);

  return routePaths.some((routePath) => directivePaths.includes(routePath));
}

function findCurrentSource(stack: StackItem[]): CaddyfileSourceBlock | undefined {
  for (let index = stack.length - 1; index >= 0; index--) {
    const source = stack[index]?.source;
    if (source) return source;
  }

  return undefined;
}

function currentMatcher(stack: StackItem[]): { matcher?: string; matcherLine?: number } {
  for (let index = stack.length - 1; index >= 0; index--) {
    const item = stack[index];
    if (item?.matcher) return { matcher: item.matcher, matcherLine: item.matcherLine };
  }

  return {};
}

function isRouteDirective(name: string | undefined): boolean {
  return Boolean(name && routeDirectiveNames.has(normalizeDirectiveName(name)));
}

function normalizeDirectiveName(name: string): string {
  if (name === "header") return "headers";
  if (name === "basicauth") return "basic_auth";

  return name;
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

function splitWords(input: string): string[] {
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

function countChar(value: string, expected: string): number {
  return [...value].filter((char) => char === expected).length;
}

function normalizeAddressForHost(address: string): string {
  let normalized = address.trim().toLowerCase();
  normalized = normalized.replace(/^https?:\/\//, "");
  normalized = normalized.split("/")[0] || normalized;
  if (normalized.startsWith(":")) return "";
  if (normalized.startsWith("*.")) normalized = normalized.slice(2);
  normalized = normalized.replace(/:\d+$/, "");

  return normalized;
}

function normalizeAddressForListener(address: string): string {
  const trimmed = address.trim().toLowerCase();
  if (trimmed.startsWith(":")) return trimmed;

  const match = trimmed.match(/:(\d+)$/);
  return match?.[1] ? `:${match[1]}` : "";
}

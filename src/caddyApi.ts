export type CaddyConfigLoadResult = {
  adminUrl: string;
  endpoint: string;
  ok: boolean;
  config?: unknown;
  error?: string;
  statusCode?: number;
  statusText?: string;
  fetchedAt: Date;
  durationMs: number;
};

export const DEFAULT_ADMIN_API_URL = "http://localhost:2019";

export type ExplicitAdminApiBaseUrl = {
  url: string;
  source: "cli" | "env";
};

export function getExplicitAdminApiBaseUrl(
  argv: string[] = Bun.argv,
  env: NodeJS.ProcessEnv = process.env,
): ExplicitAdminApiBaseUrl | undefined {
  const cliUrl = readCliAdminUrl(argv);
  if (cliUrl) return { url: normalizeAdminUrl(cliUrl), source: "cli" };

  const envUrl = env.CADDY_ADMIN_API || env.CADDY_ADMIN_URL;
  if (envUrl) return { url: normalizeAdminUrl(envUrl), source: "env" };

  return undefined;
}

export function getAdminApiBaseUrl(argv: string[] = Bun.argv, env: NodeJS.ProcessEnv = process.env): string {
  return getExplicitAdminApiBaseUrl(argv, env)?.url || DEFAULT_ADMIN_API_URL;
}

export async function fetchActiveCaddyConfig(
  adminUrl: string = getAdminApiBaseUrl(),
  timeoutMs = 2000,
): Promise<CaddyConfigLoadResult> {
  const normalizedAdminUrl = normalizeAdminUrl(adminUrl);
  const endpoint = `${normalizedAdminUrl}/config/`;
  const startedAt = performance.now();
  const fetchedAt = new Date();
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);

  try {
    const response = await fetch(endpoint, {
      headers: { accept: "application/json" },
      signal: controller.signal,
    });

    const durationMs = Math.round(performance.now() - startedAt);

    if (!response.ok) {
      const body = await safeReadBody(response);
      return {
        adminUrl: normalizedAdminUrl,
        endpoint,
        ok: false,
        error: `GET ${endpoint} returned HTTP ${response.status} ${response.statusText}${body ? `: ${body}` : ""}`,
        statusCode: response.status,
        statusText: response.statusText,
        fetchedAt,
        durationMs,
      };
    }

    try {
      return {
        adminUrl: normalizedAdminUrl,
        endpoint,
        ok: true,
        config: await response.json(),
        statusCode: response.status,
        statusText: response.statusText,
        fetchedAt,
        durationMs,
      };
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return {
        adminUrl: normalizedAdminUrl,
        endpoint,
        ok: false,
        error: `GET ${endpoint} succeeded, but the response was not valid JSON: ${message}`,
        statusCode: response.status,
        statusText: response.statusText,
        fetchedAt,
        durationMs,
      };
    }
  } catch (error) {
    const durationMs = Math.round(performance.now() - startedAt);
    const message = error instanceof Error ? error.message : String(error);
    const isAbort = error instanceof Error && error.name === "AbortError";

    return {
      adminUrl: normalizedAdminUrl,
      endpoint,
      ok: false,
      error: isAbort
        ? `Timed out connecting to Caddy Admin API at ${endpoint} after ${timeoutMs}ms`
        : `Could not connect to Caddy Admin API at ${endpoint}: ${message}`,
      fetchedAt,
      durationMs,
    };
  } finally {
    clearTimeout(timeout);
  }
}

function readCliAdminUrl(argv: string[]): string | undefined {
  for (let index = 2; index < argv.length; index++) {
    const arg = argv[index];
    if (!arg) continue;

    if (arg === "--admin" || arg === "--admin-url") {
      return argv[index + 1];
    }

    if (arg.startsWith("--admin=")) {
      return arg.slice("--admin=".length);
    }

    if (arg.startsWith("--admin-url=")) {
      return arg.slice("--admin-url=".length);
    }
  }

  return undefined;
}

export function normalizeAdminUrl(url: string): string {
  const trimmed = url.trim();
  const withProtocol = /^https?:\/\//.test(trimmed) ? trimmed : `http://${trimmed}`;

  return withProtocol.replace(/\/+$/, "");
}

async function safeReadBody(response: Response): Promise<string> {
  try {
    return (await response.text()).trim().slice(0, 500);
  } catch {
    return "";
  }
}

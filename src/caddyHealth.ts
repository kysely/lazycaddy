import { Socket, createConnection } from "node:net";
import { type CaddySource } from "./caddyConfig.ts";

export type UpstreamHealthStatus = "ok" | "down" | "unsupported";

export type UpstreamHealthResult = {
  upstream: string;
  status: UpstreamHealthStatus;
  target?: string;
  latencyMs?: number;
  error?: string;
  checkedAt: Date;
};

type DialTarget = {
  host: string;
  port: number;
  label: string;
};

export async function checkSourceUpstreams(
  source: CaddySource | undefined,
  timeoutMs = 1000,
): Promise<UpstreamHealthResult[]> {
  if (!source) return [];

  const upstreams = uniqueUpstreams(source);

  return Promise.all(
    upstreams.map(async (upstream) => {
      const dial = upstream.dial || upstream.label;
      const target = parseDialTarget(dial);

      if (!target) {
        return {
          upstream: upstream.label,
          status: "unsupported",
          error: upstream.dynamic ? "dynamic upstream" : `unsupported dial address: ${dial}`,
          checkedAt: new Date(),
        } satisfies UpstreamHealthResult;
      }

      return checkTcpTarget(upstream.label, target, timeoutMs);
    }),
  );
}

function uniqueUpstreams(source: CaddySource): Array<{ label: string; dial?: string; dynamic?: boolean }> {
  const upstreams = new Map<string, { label: string; dial?: string; dynamic?: boolean }>();

  for (const route of source.routes) {
    for (const action of route.actions) {
      for (const upstream of action.upstreams || []) {
        upstreams.set(upstream.label, {
          label: upstream.label,
          dial: upstream.dial,
          dynamic: upstream.dynamic,
        });
      }
    }
  }

  return [...upstreams.values()];
}

function parseDialTarget(dial: string): DialTarget | undefined {
  const trimmed = dial.trim();
  if (!trimmed) return undefined;
  if (trimmed.startsWith("unix/") || trimmed.startsWith("unix:")) return undefined;

  try {
    const url = /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed)
      ? new URL(trimmed)
      : new URL(`tcp://${trimmed.startsWith(":") ? `localhost${trimmed}` : trimmed}`);
    const protocol = url.protocol.replace(":", "");
    const port = Number(url.port || defaultPort(protocol));

    if (!url.hostname || !Number.isInteger(port) || port <= 0) return undefined;

    return {
      host: url.hostname,
      port,
      label: `${url.hostname}:${port}`,
    };
  } catch {
    return undefined;
  }
}

function defaultPort(protocol: string): string | undefined {
  switch (protocol) {
    case "http":
    case "h2c":
      return "80";
    case "https":
      return "443";
    default:
      return undefined;
  }
}

async function checkTcpTarget(
  upstream: string,
  target: DialTarget,
  timeoutMs: number,
): Promise<UpstreamHealthResult> {
  const checkedAt = new Date();
  const startedAt = performance.now();

  return new Promise((resolve) => {
    let settled = false;
    const socket: Socket = createConnection({ host: target.host, port: target.port });

    const finish = (result: Omit<UpstreamHealthResult, "upstream" | "target" | "checkedAt">): void => {
      if (settled) return;
      settled = true;
      socket.destroy();
      resolve({
        upstream,
        target: target.label,
        checkedAt,
        ...result,
      });
    };

    socket.setTimeout(timeoutMs);
    socket.once("connect", () => {
      finish({ status: "ok", latencyMs: Math.round(performance.now() - startedAt) });
    });
    socket.once("timeout", () => {
      finish({ status: "down", latencyMs: Math.round(performance.now() - startedAt), error: "timeout" });
    });
    socket.once("error", (error) => {
      finish({ status: "down", latencyMs: Math.round(performance.now() - startedAt), error: error.message });
    });
  });
}

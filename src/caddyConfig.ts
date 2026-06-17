type JsonObject = Record<string, unknown>;

type RouteContext = {
  serverName: string;
  listen: string[];
  hosts: string[];
  matchers: string[];
  fallbackAfter: string[];
};

type SourceRouteContext = {
  serverName: string;
  listen: string[];
  hosts: string[];
  matchers: string[];
  matcherOverride?: string;
  accessLogResolver?: (hosts: string[]) => CaddyAccessLog[];
};

export type CaddyUpstream = {
  label: string;
  dial?: string;
  dynamic?: boolean;
  raw?: unknown;
};

export type ReverseProxyRoute = {
  id: string;
  serverName: string;
  listen: string[];
  hosts: string[];
  matchers: string[];
  fallbackAfter: string[];
  upstreams: CaddyUpstream[];
  routePath: string;
  handler: JsonObject;
  transport?: string;
  loadBalancing?: string;
};

export type CaddyRouteAction = {
  kind: string;
  label: string;
  upstreams?: CaddyUpstream[];
  transport?: string;
  loadBalancing?: string;
  raw?: JsonObject;
};

export type CaddyRouteRule = {
  matcher: string;
  actions: CaddyRouteAction[];
  routePath: string;
};

export type CaddyAccessLog = {
  loggerName: string;
  loggerId?: string;
  writerOutput: string;
  filename?: string;
  encoder?: string;
  source: string;
};

export type CaddySource = {
  id: string;
  serverName: string;
  listen: string[];
  hosts: string[];
  routes: CaddyRouteRule[];
  proxyCount: number;
  accessLogs: CaddyAccessLog[];
};


export function extractReverseProxies(config: unknown): ReverseProxyRoute[] {
  const proxies: ReverseProxyRoute[] = [];
  const servers = getObject(config, "apps", "http", "servers");

  if (!servers) return proxies;

  for (const [serverName, serverValue] of Object.entries(servers)) {
    if (!isObject(serverValue)) continue;

    const listen = stringArray(serverValue.listen);
    const routes = arrayValue(serverValue.routes);
    const context: RouteContext = {
      serverName,
      listen,
      hosts: [],
      matchers: [],
      fallbackAfter: [],
    };

    traverseRoutes(routes, context, `apps.http.servers.${serverName}.routes`, proxies);

    const errorRoutes = arrayValue(getObject(serverValue, "errors")?.routes);
    traverseRoutes(errorRoutes, context, `apps.http.servers.${serverName}.errors.routes`, proxies);
  }

  return proxies;
}

export function siteLabel(proxy: ReverseProxyRoute): string {
  if (proxy.hosts.length > 0) return proxy.hosts.join(", ");
  if (proxy.listen.length > 0) return proxy.listen.join(", ");

  return proxy.serverName;
}

export function matcherLabel(proxy: ReverseProxyRoute): string {
  const positiveMatcher = proxy.matchers.length > 0 ? proxy.matchers.join("; ") : "all requests";

  if (proxy.fallbackAfter.length === 0) return positiveMatcher;

  const priorMatchers = proxy.fallbackAfter.join("; ");

  return proxy.matchers.length > 0
    ? `${positiveMatcher} (after earlier handle routes: ${priorMatchers})`
    : `fallback after ${priorMatchers}`;
}

export function upstreamLabel(proxy: ReverseProxyRoute): string {
  return proxy.upstreams.length > 0 ? proxy.upstreams.map((upstream) => upstream.label).join(", ") : "(no upstreams)";
}

export function extractCaddySources(config: unknown): CaddySource[] {
  const sources = new Map<string, CaddySource>();
  const servers = getObject(config, "apps", "http", "servers");
  const loggingLogs = getObject(config, "logging", "logs");

  if (!servers) return [];

  for (const [serverName, serverValue] of Object.entries(servers)) {
    if (!isObject(serverValue)) continue;

    const listen = stringArray(serverValue.listen);
    const serverLogs = getObject(serverValue, "logs");
    const context: SourceRouteContext = {
      serverName,
      listen,
      hosts: [],
      matchers: [],
      accessLogResolver: (hosts) => resolveAccessLogs(serverLogs, loggingLogs, hosts),
    };

    traverseSourceRoutes(arrayValue(serverValue.routes), context, `apps.http.servers.${serverName}.routes`, sources);
  }

  return [...sources.values()].filter((source) => source.proxyCount > 0);
}

export function sourceLabel(source: CaddySource): string {
  if (source.hosts.length > 0) return source.hosts.join(", ");
  if (source.listen.length > 0) return source.listen.join(", ");

  return source.serverName;
}

export function sourceProxySummary(source: CaddySource): string {
  const upstreams = uniqueStrings(
    source.routes.flatMap((route) =>
      route.actions.flatMap((action) => action.upstreams?.map((upstream) => upstream.label) || []),
    ),
  );

  return upstreams.length > 0 ? upstreams.join(", ") : "(no upstreams)";
}

function traverseRoutes(routes: unknown[], parentContext: RouteContext, routesPath: string, proxies: ReverseProxyRoute[]): void {
  const priorMatchersByGroup = new Map<string, string[]>();

  routes.forEach((route, index) => {
    if (!isObject(route)) return;

    const group = typeof route.group === "string" ? route.group : undefined;
    const fallbackAfter = group ? priorMatchersByGroup.get(group) || [] : [];

    traverseRoute(route, parentContext, `${routesPath}[${index}]`, proxies, fallbackAfter);

    if (group) {
      const currentMatcher = routeMatcherLabel(route.match);
      if (currentMatcher) {
        priorMatchersByGroup.set(group, [...fallbackAfter, currentMatcher]);
      }
    }
  });
}

function traverseRoute(
  routeValue: unknown,
  parentContext: RouteContext,
  routePath: string,
  proxies: ReverseProxyRoute[],
  fallbackAfter: string[] = [],
): void {
  if (!isObject(routeValue)) return;

  const matchSummary = summarizeRouteMatch(routeValue.match);
  const context: RouteContext = {
    ...parentContext,
    hosts: uniqueStrings([...parentContext.hosts, ...matchSummary.hosts]),
    matchers: uniqueStrings([...parentContext.matchers, ...matchSummary.matchers]),
    fallbackAfter: uniqueStrings([...parentContext.fallbackAfter, ...fallbackAfter]),
  };

  const handlers = arrayValue(routeValue.handle);
  handlers.forEach((handler, index) => {
    traverseHandler(handler, context, `${routePath}.handle[${index}]`, proxies);
  });

  const nestedRoutes = arrayValue(routeValue.routes);
  traverseRoutes(nestedRoutes, context, `${routePath}.routes`, proxies);
}

function traverseHandler(
  handlerValue: unknown,
  context: RouteContext,
  handlerPath: string,
  proxies: ReverseProxyRoute[],
): void {
  if (!isObject(handlerValue)) return;

  if (handlerValue.handler === "reverse_proxy") {
    proxies.push({
      id: `${context.serverName}:${proxies.length}`,
      serverName: context.serverName,
      listen: context.listen,
      hosts: context.hosts,
      matchers: context.matchers,
      fallbackAfter: context.fallbackAfter,
      upstreams: extractUpstreams(handlerValue),
      routePath: handlerPath,
      handler: handlerValue,
      transport: summarizeTransport(handlerValue.transport),
      loadBalancing: summarizeLoadBalancing(handlerValue),
    });
  }

  const nestedRoutes = arrayValue(handlerValue.routes);
  traverseRoutes(nestedRoutes, context, `${handlerPath}.routes`, proxies);

  const nestedHandlers = arrayValue(handlerValue.handle);
  nestedHandlers.forEach((handler, index) => {
    traverseHandler(handler, context, `${handlerPath}.handle[${index}]`, proxies);
  });
}

function traverseSourceRoutes(
  routes: unknown[],
  parentContext: SourceRouteContext,
  routesPath: string,
  sources: Map<string, CaddySource>,
): void {
  const priorMatchersByGroup = new Map<string, string[]>();

  routes.forEach((route, index) => {
    if (!isObject(route)) return;

    const group = typeof route.group === "string" ? route.group : undefined;
    const ownMatchSummary = summarizeRouteMatch(route.match);
    const priorMatchers = group ? priorMatchersByGroup.get(group) || [] : [];
    const isFallbackRoute = priorMatchers.length > 0 && ownMatchSummary.matchers.length === 0;

    traverseSourceRoute(route, parentContext, `${routesPath}[${index}]`, sources, isFallbackRoute);

    if (group && ownMatchSummary.matchers.length > 0) {
      priorMatchersByGroup.set(group, [...priorMatchers, ownMatchSummary.matchers.join("; ")]);
    }
  });
}

function traverseSourceRoute(
  routeValue: unknown,
  parentContext: SourceRouteContext,
  routePath: string,
  sources: Map<string, CaddySource>,
  isFallbackRoute = false,
): void {
  if (!isObject(routeValue)) return;

  const matchSummary = summarizeRouteMatch(routeValue.match);
  const context: SourceRouteContext = {
    ...parentContext,
    hosts: uniqueStrings([...parentContext.hosts, ...matchSummary.hosts]),
    matchers: uniqueStrings([...parentContext.matchers, ...matchSummary.matchers]),
    matcherOverride: isFallbackRoute ? "everything else" : parentContext.matcherOverride,
  };
  const handlers = arrayValue(routeValue.handle);
  const actions = summarizeDirectRouteActions(handlers);

  if (actions.length > 0) {
    addSourceRoute(sources, context, {
      matcher: sourceRouteMatcherLabel(context.matchers, context.matcherOverride),
      actions,
      routePath,
    });
  }

  handlers.forEach((handler, index) => {
    if (!isObject(handler)) return;

    traverseSourceRoutes(arrayValue(handler.routes), context, `${routePath}.handle[${index}].routes`, sources);

    const nestedHandlers = arrayValue(handler.handle);
    nestedHandlers.forEach((nestedHandler, nestedIndex) => {
      if (!isObject(nestedHandler)) return;
      traverseSourceRoutes(
        arrayValue(nestedHandler.routes),
        context,
        `${routePath}.handle[${index}].handle[${nestedIndex}].routes`,
        sources,
      );
    });
  });

  traverseSourceRoutes(arrayValue(routeValue.routes), context, `${routePath}.routes`, sources);
}

function summarizeDirectRouteActions(handlers: unknown[]): CaddyRouteAction[] {
  const actions: CaddyRouteAction[] = [];
  let root: string | undefined;

  for (const handler of handlers) {
    if (!isObject(handler)) continue;

    const handlerName = typeof handler.handler === "string" ? handler.handler : undefined;

    switch (handlerName) {
      case "vars":
        if (typeof handler.root === "string") {
          root = handler.root;
        } else {
          actions.push({ kind: "vars", label: summarizeVarsHandler(handler), raw: handler });
        }
        break;
      case "map":
        actions.push({ kind: "map", label: summarizeMapHandler(handler), raw: handler });
        break;
      case "file_server":
        actions.push({ kind: "file_server", label: summarizeFileServerHandler(handler, root), raw: handler });
        break;
      case "reverse_proxy":
        actions.push(summarizeReverseProxyAction(handler));
        break;
      case "static_response":
      case "respond":
        actions.push(summarizeStaticResponseAction(handler));
        break;
      case "rewrite":
        actions.push({ kind: "rewrite", label: summarizeRewriteHandler(handler), raw: handler });
        break;
      case "headers":
        actions.push({ kind: "headers", label: summarizeHeadersHandler(handler), raw: handler });
        break;
      case "encode":
        actions.push({ kind: "encode", label: summarizeEncodeHandler(handler), raw: handler });
        break;
      case "authentication":
        actions.push({ kind: "authentication", label: summarizeAuthenticationHandler(handler), raw: handler });
        break;
      case "request_body":
        actions.push({ kind: "request_body", label: summarizeRequestBodyHandler(handler), raw: handler });
        break;
      case "templates":
        actions.push({ kind: "templates", label: summarizeTemplatesHandler(handler), raw: handler });
        break;
      case "error":
        actions.push({ kind: "error", label: summarizeErrorHandler(handler), raw: handler });
        break;
      case "metrics":
        actions.push({ kind: "metrics", label: "metrics", raw: handler });
        break;
      case "acme_server":
        actions.push({ kind: "acme_server", label: "acme_server", raw: handler });
        break;
      case "subroute":
      case undefined:
        break;
      default:
        actions.push({ kind: handlerName, label: summarizeUnknownHandler(handlerName, handler), raw: handler });
        break;
    }
  }

  return actions;
}

function addSourceRoute(sources: Map<string, CaddySource>, context: SourceRouteContext, route: CaddyRouteRule): void {
  const key = sourceKey(context);
  const resolvedAccessLogs = context.accessLogResolver?.(context.hosts) || [];
  const source = sources.get(key) || {
    id: key,
    serverName: context.serverName,
    listen: context.listen,
    hosts: context.hosts,
    routes: [],
    proxyCount: 0,
    accessLogs: resolvedAccessLogs,
  };

  source.accessLogs = mergeAccessLogs(source.accessLogs, resolvedAccessLogs);
  source.routes.push(route);
  source.proxyCount += route.actions.filter((action) => action.kind === "reverse_proxy").length;
  sources.set(key, source);
}

function resolveAccessLogs(
  serverLogs: JsonObject | undefined,
  loggingLogs: JsonObject | undefined,
  hosts: string[],
): CaddyAccessLog[] {
  if (!serverLogs) return [];

  const skippedHosts = stringArray(serverLogs.skip_hosts);
  const loggerNames = getObject(serverLogs, "logger_names");
  const defaultLoggerName = typeof serverLogs.default_logger_name === "string" ? serverLogs.default_logger_name : undefined;
  const resolvedLoggerNames: Array<{ loggerName: string; source: string }> = [];

  for (const host of hosts) {
    if (skippedHosts.includes(host)) continue;

    const loggerName = typeof loggerNames?.[host] === "string" ? loggerNames[host] : undefined;
    if (loggerName !== undefined) {
      resolvedLoggerNames.push({ loggerName, source: `host ${host}` });
    }
  }

  if (resolvedLoggerNames.length === 0 && defaultLoggerName !== undefined) {
    resolvedLoggerNames.push({ loggerName: defaultLoggerName, source: "server default" });
  }

  return mergeAccessLogs(
    resolvedLoggerNames.map(({ loggerName, source }) => buildAccessLog(loggerName, source, loggingLogs?.[loggerName])),
    [],
  );
}

function buildAccessLog(loggerName: string, source: string, logConfig: unknown): CaddyAccessLog {
  const config = isObject(logConfig) ? logConfig : undefined;
  const writer = getObject(config, "writer");
  const encoder = getObject(config, "encoder");
  const include = stringArray(config?.include);

  return {
    loggerName: loggerName || "default",
    loggerId: include.find((entry) => entry.startsWith("http.log.access.")) || (loggerName ? `http.log.access.${loggerName}` : undefined),
    writerOutput: typeof writer?.output === "string" ? writer.output : "default",
    filename: typeof writer?.filename === "string" ? writer.filename : undefined,
    encoder: typeof encoder?.format === "string" ? encoder.format : undefined,
    source,
  };
}

function mergeAccessLogs(left: CaddyAccessLog[], right: CaddyAccessLog[]): CaddyAccessLog[] {
  const merged = new Map<string, CaddyAccessLog>();

  for (const log of [...left, ...right]) {
    const key = `${log.loggerName}:${log.writerOutput}:${log.filename || ""}`;
    merged.set(key, log);
  }

  return [...merged.values()];
}

function sourceKey(context: SourceRouteContext): string {
  const address = context.hosts.length > 0 ? context.hosts.join(",") : context.listen.join(",") || context.serverName;

  return `${context.serverName}:${address}`;
}

function sourceRouteMatcherLabel(matchers: string[], matcherOverride?: string): string {
  if (matcherOverride) return matcherOverride;

  return matchers.length > 0 ? matchers.join("; ") : "all requests";
}

function summarizeReverseProxyAction(handler: JsonObject): CaddyRouteAction {
  const upstreams = extractUpstreams(handler);
  const upstreamLabel = upstreams.length > 0 ? upstreams.map((upstream) => upstream.label).join(", ") : "(no upstreams)";
  const transport = summarizeTransport(handler.transport);
  const isFastCgi = isObject(handler.transport) && handler.transport.protocol === "fastcgi";

  return {
    kind: "reverse_proxy",
    label: isFastCgi ? `php_fastcgi ${upstreamLabel}` : `reverse_proxy ${upstreamLabel}`,
    upstreams,
    transport,
    loadBalancing: summarizeLoadBalancing(handler),
    raw: handler,
  };
}

function summarizeStaticResponseAction(handler: JsonObject): CaddyRouteAction {
  const statusCode = numberOrUndefined(handler.status_code);
  const location = firstHeaderValue(handler.headers, "Location");

  if (location && statusCode && statusCode >= 300 && statusCode < 400) {
    return { kind: "redir", label: `redir ${location} ${statusCode}`, raw: handler };
  }

  const body = typeof handler.body === "string" && handler.body.length > 0 ? ` ${JSON.stringify(truncate(handler.body, 40))}` : "";
  const status = statusCode ? ` ${statusCode}` : "";

  return { kind: "respond", label: `respond${status}${body}`, raw: handler };
}

function summarizeFileServerHandler(handler: JsonObject, root: string | undefined): string {
  const parts = ["file_server"];
  if (root) parts.push(root);
  if (handler.browse) parts.push("browse");
  if (handler.pass_thru) parts.push("pass_thru");
  const indexNames = stringArray(handler.index_names);
  if (indexNames.length > 0) parts.push(`index ${indexNames.join(",")}`);

  return parts.join(" ");
}

function summarizeRewriteHandler(handler: JsonObject): string {
  if (typeof handler.uri === "string") return `rewrite ${handler.uri}`;
  if (typeof handler.strip_path_prefix === "string") return `uri strip_prefix ${handler.strip_path_prefix}`;
  if (typeof handler.strip_path_suffix === "string") return `uri strip_suffix ${handler.strip_path_suffix}`;
  if (isObject(handler.uri_substring)) {
    const find = typeof handler.uri_substring.find === "string" ? handler.uri_substring.find : "";
    const replace = typeof handler.uri_substring.replace === "string" ? handler.uri_substring.replace : "";
    return `uri replace ${find} ${replace}`.trim();
  }
  if (isObject(handler.path_regexp)) return `rewrite path_regexp ${summarizeRegexpMatcher(handler.path_regexp)}`;

  return `rewrite ${compactJson(omitHandlerName(handler))}`;
}

function summarizeHeadersHandler(handler: JsonObject): string {
  const operations: string[] = [];

  collectHeaderOperations(operations, "request", getObject(handler, "request"));
  collectHeaderOperations(operations, "response", getObject(handler, "response"));

  return operations.length > 0 ? `headers ${operations.join("; ")}` : "headers";
}

function collectHeaderOperations(operations: string[], scope: string, config: JsonObject | undefined): void {
  if (!config) return;

  for (const operation of ["set", "add", "delete", "replace", "defer"] as const) {
    const value = config[operation];
    if (value === undefined) continue;

    if (isObject(value)) {
      operations.push(`${scope}.${operation} ${Object.keys(value).join(",")}`);
    } else if (Array.isArray(value)) {
      operations.push(`${scope}.${operation} ${value.join(",")}`);
    } else {
      operations.push(`${scope}.${operation}`);
    }
  }
}

function summarizeEncodeHandler(handler: JsonObject): string {
  const encodings = isObject(handler.encodings) ? Object.keys(handler.encodings) : [];
  const prefer = stringArray(handler.prefer);
  const labels = prefer.length > 0 ? prefer : encodings;

  return labels.length > 0 ? `encode ${labels.join(", ")}` : "encode";
}

function summarizeAuthenticationHandler(handler: JsonObject): string {
  const providers = getObject(handler, "providers");
  if (!providers) return "authentication";

  const httpBasic = getObject(providers, "http_basic");
  if (httpBasic) {
    const accounts = arrayValue(httpBasic.accounts);
    const realm = typeof httpBasic.realm === "string" ? ` realm=${httpBasic.realm}` : "";
    return `basic_auth ${accounts.length} user${accounts.length === 1 ? "" : "s"}${realm}`;
  }

  return `authentication ${Object.keys(providers).join(", ")}`;
}

function summarizeRequestBodyHandler(handler: JsonObject): string {
  const parts = ["request_body"];
  if (handler.max_size !== undefined) parts.push(`max_size ${formatByteLimit(handler.max_size)}`);

  return parts.join(" ");
}

function summarizeTemplatesHandler(handler: JsonObject): string {
  const mimeTypes = stringArray(handler.mime_types);

  return mimeTypes.length > 0 ? `templates ${mimeTypes.join(", ")}` : "templates";
}

function summarizeMapHandler(handler: JsonObject): string {
  const source = typeof handler.source === "string" ? handler.source : "source";
  const destinations = stringArray(handler.destinations);
  const defaults = arrayValue(handler.defaults).map(String);
  const destination = destinations.length > 0 ? destinations.join(",") : "destination";
  const suffix = defaults.length > 0 ? ` default ${defaults.join(",")}` : "";

  return `map ${source} → ${destination}${suffix}`;
}

function summarizeVarsHandler(handler: JsonObject): string {
  const entries = Object.entries(omitHandlerName(handler)).map(([key, value]) => `${key}=${compactJson(value)}`);

  return entries.length > 0 ? `vars ${entries.join(" ")}` : "vars";
}

function summarizeErrorHandler(handler: JsonObject): string {
  const status = numberOrUndefined(handler.status_code);
  const message = typeof handler.message === "string" ? ` ${JSON.stringify(handler.message)}` : "";

  return `error${status ? ` ${status}` : ""}${message}`;
}

function summarizeUnknownHandler(handlerName: string | undefined, handler: JsonObject): string {
  const name = handlerName || "handler";
  const summary = compactJson(omitHandlerName(handler));

  return summary === "{}" ? name : `${name} ${summary}`;
}

function extractUpstreams(handler: JsonObject): CaddyUpstream[] {
  const upstreams: CaddyUpstream[] = [];

  for (const upstream of arrayValue(handler.upstreams)) {
    if (typeof upstream === "string") {
      upstreams.push({ label: upstream, dial: upstream, raw: upstream });
      continue;
    }

    if (!isObject(upstream)) {
      upstreams.push({ label: compactJson(upstream), raw: upstream });
      continue;
    }

    const dial = typeof upstream.dial === "string" ? upstream.dial : undefined;
    upstreams.push({
      label: dial || compactJson(upstream),
      dial,
      raw: upstream,
    });
  }

  const dynamicUpstreams = handler.dynamic_upstreams;
  if (dynamicUpstreams !== undefined) {
    upstreams.push({
      label: `dynamic ${summarizeDynamicUpstreams(dynamicUpstreams)}`,
      dynamic: true,
      raw: dynamicUpstreams,
    });
  }

  return upstreams;
}

function routeMatcherLabel(matchValue: unknown): string | undefined {
  const summary = summarizeRouteMatch(matchValue);
  const labels = [...summary.hosts.map((host) => `host ${host}`), ...summary.matchers];

  return labels.length > 0 ? labels.join("; ") : undefined;
}

function summarizeRouteMatch(matchValue: unknown): { hosts: string[]; matchers: string[] } {
  const matchSets = Array.isArray(matchValue) ? matchValue : isObject(matchValue) ? [matchValue] : [];
  const hosts: string[] = [];
  const matchers: string[] = [];

  for (const matchSet of matchSets) {
    if (!isObject(matchSet)) continue;

    const parts: string[] = [];

    for (const [key, value] of Object.entries(matchSet)) {
      switch (key) {
        case "host":
          hosts.push(...stringArray(value));
          break;
        case "path":
          parts.push(`path ${stringArray(value).join(", ") || compactJson(value)}`);
          break;
        case "path_regexp":
          parts.push(`path_regexp ${summarizeRegexpMatcher(value)}`);
          break;
        case "method":
          parts.push(`method ${stringArray(value).join(", ") || compactJson(value)}`);
          break;
        case "header":
          parts.push(`header ${summarizeObjectMatcher(value)}`);
          break;
        case "query":
          parts.push(`query ${summarizeObjectMatcher(value)}`);
          break;
        case "file":
          parts.push(`file ${summarizeFileMatcher(value)}`);
          break;
        case "not":
          parts.push(`not ${summarizeNotMatcher(value)}`);
          break;
        case "remote_ip":
          parts.push(`remote_ip ${stringArray(value).join(", ") || compactJson(value)}`);
          break;
        case "protocol":
          parts.push(`protocol ${typeof value === "string" ? value : compactJson(value)}`);
          break;
        case "vars":
          parts.push(`vars ${summarizeObjectMatcher(value)}`);
          break;
        case "expression":
          parts.push(`expr ${typeof value === "string" ? value : compactJson(value)}`);
          break;
        default:
          parts.push(`${key} ${compactJson(value)}`);
          break;
      }
    }

    if (parts.length > 0) matchers.push(parts.join(" + "));
  }

  return {
    hosts: uniqueStrings(hosts),
    matchers: uniqueStrings(matchers),
  };
}

function summarizeTransport(transport: unknown): string | undefined {
  if (!isObject(transport)) return undefined;

  const protocol = typeof transport.protocol === "string" ? transport.protocol : undefined;
  const tls = transport.tls === true ? " + tls" : "";

  return protocol ? `${protocol}${tls}` : compactJson(transport);
}

function summarizeLoadBalancing(handler: JsonObject): string | undefined {
  const policy = getObject(handler, "load_balancing", "selection_policy");
  if (!policy) return undefined;

  const policyName = typeof policy.policy === "string" ? policy.policy : undefined;
  return policyName || compactJson(policy);
}

function summarizeFileMatcher(value: unknown): string {
  if (!isObject(value)) return compactJson(value);

  const parts: string[] = [];
  const tryFiles = stringArray(value.try_files);
  const splitPath = stringArray(value.split_path);

  if (tryFiles.length > 0) parts.push(`try_files ${tryFiles.join(", ")}`);
  if (splitPath.length > 0) parts.push(`split_path ${splitPath.join(", ")}`);
  if (value.root) parts.push(`root ${compactJson(value.root)}`);

  return parts.length > 0 ? parts.join("; ") : compactJson(value);
}

function summarizeNotMatcher(value: unknown): string {
  const matchSets = Array.isArray(value) ? value : isObject(value) ? [value] : [];
  if (matchSets.length === 0) return compactJson(value);

  return matchSets.map((matchSet) => summarizeRouteMatch(matchSet).matchers.join(" + ") || compactJson(matchSet)).join("; ");
}

function summarizeRegexpMatcher(value: unknown): string {
  if (!isObject(value)) return compactJson(value);

  const name = typeof value.name === "string" ? `${value.name}: ` : "";
  const pattern = typeof value.pattern === "string" ? value.pattern : compactJson(value);

  return `${name}${pattern}`;
}

function summarizeObjectMatcher(value: unknown): string {
  if (!isObject(value)) return compactJson(value);

  return Object.entries(value)
    .map(([key, matcherValue]) => `${key}=${Array.isArray(matcherValue) ? stringArray(matcherValue).join(",") : compactJson(matcherValue)}`)
    .join("; ");
}

function summarizeDynamicUpstreams(value: unknown): string {
  if (Array.isArray(value)) return value.map(summarizeDynamicUpstreams).join(", ");
  if (!isObject(value)) return compactJson(value);

  const source = typeof value.source === "string" ? value.source : undefined;
  const name = typeof value.name === "string" ? value.name : undefined;

  return source || name || compactJson(value);
}

function getObject(value: unknown, ...path: string[]): JsonObject | undefined {
  let current = value;

  for (const segment of path) {
    if (!isObject(current)) return undefined;
    current = current[segment];
  }

  return isObject(current) ? current : undefined;
}

function arrayValue(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function stringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return typeof value === "string" ? [value] : [];

  return value.filter((item): item is string => typeof item === "string");
}

function isObject(value: unknown): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function uniqueStrings(values: string[]): string[] {
  return [...new Set(values.filter(Boolean))];
}

function numberOrUndefined(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }

  return undefined;
}

function firstHeaderValue(headers: unknown, name: string): string | undefined {
  if (!isObject(headers)) return undefined;

  const key = Object.keys(headers).find((candidate) => candidate.toLowerCase() === name.toLowerCase());
  const value = key ? headers[key] : undefined;

  if (Array.isArray(value)) return value.find((item): item is string => typeof item === "string");
  return typeof value === "string" ? value : undefined;
}

function omitHandlerName(handler: JsonObject): JsonObject {
  const { handler: _handler, ...rest } = handler;

  return rest;
}

function truncate(value: string, maxLength: number): string {
  if (value.length <= maxLength) return value;
  if (maxLength <= 1) return "…";

  return `${value.slice(0, maxLength - 1)}…`;
}

function formatByteLimit(value: unknown): string {
  const bytes = numberOrUndefined(value);
  if (bytes === undefined) return compactJson(value);
  if (bytes < 1000) return `${bytes}B`;
  if (bytes < 1000 * 1000) return `${(bytes / 1000).toFixed(1)}KB`;
  if (bytes < 1000 * 1000 * 1000) return `${(bytes / 1000 / 1000).toFixed(1)}MB`;

  return `${(bytes / 1000 / 1000 / 1000).toFixed(1)}GB`;
}

function compactJson(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === undefined) return "undefined";

  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

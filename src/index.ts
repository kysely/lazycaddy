import {
  BoxRenderable,
  createCliRenderer,
  ScrollBoxRenderable,
  StyledText,
  TextAttributes,
  TextRenderable,
  bg,
  bold,
  fg,
  type TextChunk,
} from "@opentui/core";
import { fetchActiveCaddyConfig, type CaddyConfigLoadResult } from "./caddyApi.ts";
import {
  extractCaddySources,
  sourceLabel,
  type CaddySource,
} from "./caddyConfig.ts";
import { discoverAdminApiEndpoint } from "./caddyDiscovery.ts";
import {
  fetchAccessLogsForSource,
  fetchCaddyLogs,
  type CaddyAccessLogsResult,
  type CaddyLogsResult,
} from "./caddyLogs.ts";
import { checkSourceUpstreams, type UpstreamHealthResult } from "./caddyHealth.ts";
import { validateCaddyConfig, type CaddyValidationResult } from "./caddyCommands.ts";
import {
  isImportantAccessLogEntry,
  parseAccessLogOutput,
  type ParsedAccessLogEntry,
} from "./accessLogParser.ts";
import {
  caddyfileLocation,
  findCaddyfileSource,
  formatCaddyfileBlock,
  loadCaddyfileCorrelation,
  type CaddyfileSourceBlock,
} from "./caddyfileCorrelation.ts";
import {
  isImportantServiceLogEntry,
  parseServiceLogOutput,
  serviceLogKind,
  type ParsedServiceLogEntry,
} from "./serviceLogParser.ts";

const discovery = await discoverAdminApiEndpoint();
const caddyfileCorrelation = await loadCaddyfileCorrelation(
  discovery.configPath || discovery.command?.configPath,
  discovery.adapter || discovery.command?.adapter,
);
const adminUrl = discovery.adminUrl;
const renderer = await createCliRenderer({ exitOnCtrlC: true });

let sources: CaddySource[] = [];
let selectedIndex = -1;
let refreshing = false;
let refreshInFlight: Promise<void> | undefined;
let logsRefreshInFlight: Promise<void> | undefined;
let validationInFlight: Promise<void> | undefined;
let upstreamHealthInFlight: Promise<void> | undefined;
let logsRefreshing = false;
let validating = false;
let healthCheckingSourceId: string | undefined;
type MainView = "services" | "logs" | "logDetail";
type ActiveView = MainView | "config" | "system" | "help";
let activeView: ActiveView = "services";
let previousMainView: MainView = "services";
let previousHelpView: Exclude<ActiveView, "help"> = "services";
let previousConfigView: MainView = "logs";
type LogsTimeWindow = "day" | "week" | "all";
let logsErrorOnly = false;
let logsSlowOnly = false;
let logsTimeWindow: LogsTimeWindow = "day";
let selectedAccessLogIndex = -1;
let lastLogs: CaddyLogsResult | undefined;
let lastAccessLogs: CaddyAccessLogsResult | undefined;
let lastValidation: CaddyValidationResult | undefined;
let parsedAccessLogCache: {
  logs: CaddyAccessLogsResult | undefined;
  entries: ParsedAccessLogEntry[];
} | undefined;
let visibleAccessLogCache: {
  logs: CaddyAccessLogsResult | undefined;
  errorOnly: boolean;
  slowOnly: boolean;
  timeWindow: LogsTimeWindow;
  entries: ParsedAccessLogEntry[];
} | undefined;
type AccessLogStats = {
  available: boolean;
  ok: boolean;
  count24h?: number;
};
const accessLogStatsBySource = new Map<string, AccessLogStats>();
const upstreamHealthBySource = new Map<string, UpstreamHealthResult[]>();
const slowRequestThresholdMs = 1000;
const accessLogWindowBefore = 12;
const accessLogWindowAfter = 28;
const minAccessLogWindowRows = 8;
let lastLoad: CaddyConfigLoadResult = {
  adminUrl: adminUrl || "(not discovered)",
  endpoint: adminUrl ? `${adminUrl}/config/` : "(admin API disabled or unsupported)",
  ok: false,
  error: adminUrl ? "Waiting for first refresh" : adminUnavailableMessage(),
  fetchedAt: new Date(),
  durationMs: 0,
};

const root = new BoxRenderable(renderer, {
  flexGrow: 1,
  padding: 1,
  rowGap: 1,
  backgroundColor: "#f8fafc",
});

const header = new BoxRenderable(renderer, {
  height: 1,
  width: "100%",
});

const titleText = new TextRenderable(renderer, {
  content: "lazycaddy",
  fg: "#0369a1",
  attributes: TextAttributes.BOLD,
});

const statusText = new TextRenderable(renderer, {
  content: "●",
  fg: "#d97706",
  attributes: TextAttributes.BOLD,
});

header.add(titleText);

const tabBar = new BoxRenderable(renderer, {
  height: 1,
});

const tabText = new TextRenderable(renderer, {
  content: tabsLine(),
  fg: "#111827",
});

tabBar.add(tabText);

const body = new BoxRenderable(renderer, {
  flexDirection: "column",
  flexGrow: 1,
  rowGap: 1,
});

const proxyPanel = new BoxRenderable(renderer, {
  width: "100%",
  flexGrow: 1,
});

const servicesScroll = new ScrollBoxRenderable(renderer, {
  width: "100%",
  height: "100%",
  flexGrow: 1,
  scrollY: true,
  scrollX: false,
});

const servicesText = new TextRenderable(renderer, {
  content: "Loading active config...",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
});

servicesScroll.add(servicesText);
proxyPanel.add(servicesScroll);

const detailPanel = new BoxRenderable(renderer, {
  flexGrow: 1,
  rowGap: 1,
});

const sourceTabBox = new BoxRenderable(renderer, {
  height: 3,
});

const sourceTabText = new TextRenderable(renderer, {
  content: sourceTabsLine(),
  fg: "#111827",
});

sourceTabBox.add(sourceTabText);

const sourceBox = new BoxRenderable(renderer, {
  height: 5,
});

const sourceSectionText = new TextRenderable(renderer, {
  content: "Fetching active Caddy config...",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
  height: "100%",
});

const routesBox = new BoxRenderable(renderer, {
  flexGrow: 1,
});

const routesText = new TextRenderable(renderer, {
  content: "",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
  height: "100%",
  flexGrow: 1,
});

const reverseProxyBox = new BoxRenderable(renderer, {
  height: 5,
});

const reverseProxyText = new TextRenderable(renderer, {
  content: "",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
  height: "100%",
});

const upstreamHealthBox = new BoxRenderable(renderer, {
  height: 6,
});

const upstreamHealthText = new TextRenderable(renderer, {
  content: "",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
  height: "100%",
});

const configBox = new BoxRenderable(renderer, {
  height: 4,
});

const configText = new TextRenderable(renderer, {
  content: "",
  fg: "#475569",
  wrapMode: "word",
  width: "100%",
  height: "100%",
});

sourceBox.add(sourceSectionText);
routesBox.add(routesText);
reverseProxyBox.add(reverseProxyText);
upstreamHealthBox.add(upstreamHealthText);
configBox.add(configText);
detailPanel.add(sourceTabBox);
detailPanel.add(sourceBox);
detailPanel.add(routesBox);
detailPanel.add(reverseProxyBox);
detailPanel.add(upstreamHealthBox);
detailPanel.add(configBox);
body.add(proxyPanel);
body.add(detailPanel);

const serviceView = new BoxRenderable(renderer, {
  visible: false,
  flexGrow: 1,
  rowGap: 1,
});

const serviceStatusBox = new BoxRenderable(renderer, {
  height: 7,
});

const serviceStatusText = new TextRenderable(renderer, {
  content: "Loading service status...",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
  height: "100%",
});

const serviceConfigBox = new BoxRenderable(renderer, {
  height: 6,
});

const serviceConfigText = new TextRenderable(renderer, {
  content: "",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
  height: "100%",
});

const serviceValidationBox = new BoxRenderable(renderer, {
  height: 6,
});

const serviceValidationText = new TextRenderable(renderer, {
  content: "Not run yet. Press v to validate config.",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
  height: "100%",
});

const serviceAdminBox = new BoxRenderable(renderer, {
  height: 7,
});

const serviceAdminText = new TextRenderable(renderer, {
  content: "",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
  height: "100%",
});

const serviceDiscoveryBox = new BoxRenderable(renderer, {
  height: 5,
});

const serviceDiscoveryText = new TextRenderable(renderer, {
  content: "",
  fg: "#475569",
  wrapMode: "word",
  width: "100%",
  height: "100%",
  flexGrow: 1,
});

const systemScroll = new ScrollBoxRenderable(renderer, {
  width: "100%",
  height: "100%",
  flexGrow: 1,
  scrollY: true,
  scrollX: false,
});

const systemText = new TextRenderable(renderer, {
  content: "",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
});

serviceStatusBox.add(serviceStatusText);
serviceConfigBox.add(serviceConfigText);
serviceValidationBox.add(serviceValidationText);
serviceAdminBox.add(serviceAdminText);
serviceDiscoveryBox.add(serviceDiscoveryText);
systemScroll.add(systemText);
serviceView.add(systemScroll);

const logsView = new BoxRenderable(renderer, {
  visible: false,
  flexGrow: 1,
  rowGap: 1,
});

const logsStatusBox = new BoxRenderable(renderer, {
  height: 3,
});

const logsStatusText = new TextRenderable(renderer, {
  content: "Loading recent caddy.service logs...",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
  height: "100%",
});

const accessLogsRow = new BoxRenderable(renderer, {
  flexDirection: "column",
  flexGrow: 1,
});

const accessLogsBox = new BoxRenderable(renderer, {
  width: "100%",
  flexGrow: 1,
});

const accessLogsFilterBar = new BoxRenderable(renderer, {
  width: "100%",
  height: 1,
  flexDirection: "row",
  justifyContent: "space-between",
});

const accessLogsTimeFilterText = new TextRenderable(renderer, {
  content: "",
  fg: "#64748b",
  wrapMode: "none",
  width: "auto",
  height: 1,
});

const accessLogsKindFilterText = new TextRenderable(renderer, {
  content: "",
  fg: "#64748b",
  wrapMode: "none",
  width: "auto",
  height: 1,
});

const accessLogsHeaderText = new TextRenderable(renderer, {
  content: "",
  fg: "#64748b",
  wrapMode: "none",
  width: "100%",
  height: 1,
});

accessLogsFilterBar.add(accessLogsTimeFilterText);
accessLogsFilterBar.add(accessLogsKindFilterText);

const accessLogsScroll = new ScrollBoxRenderable(renderer, {
  width: "100%",
  height: "auto",
  flexGrow: 1,
  stickyScroll: false,
  stickyStart: "bottom",
  scrollY: false,
  scrollX: false,
  verticalScrollbarOptions: { visible: false },
  horizontalScrollbarOptions: { visible: false },
});

const accessLogsPageText = new TextRenderable(renderer, {
  content: "",
  fg: "#64748b",
  wrapMode: "none",
  width: "100%",
  height: 1,
});

const accessLogsText = new TextRenderable(renderer, {
  content: "",
  fg: "#111827",
  wrapMode: "none",
  truncate: true,
  width: "100%",
});

const accessLogDetailBox = new BoxRenderable(renderer, {
  visible: false,
  width: "100%",
  flexGrow: 1,
});

const accessLogDetailScroll = new ScrollBoxRenderable(renderer, {
  width: "100%",
  height: "100%",
  flexGrow: 1,
  scrollY: true,
  scrollX: false,
});

const accessLogDetailText = new TextRenderable(renderer, {
  content: "",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
});

const logsBox = new BoxRenderable(renderer, {
  flexGrow: 1,
});

const logsScroll = new ScrollBoxRenderable(renderer, {
  width: "100%",
  height: "100%",
  flexGrow: 1,
  stickyScroll: true,
  stickyStart: "bottom",
  scrollY: true,
  scrollX: false,
});

const logsText = new TextRenderable(renderer, {
  content: "",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
});

accessLogsScroll.add(accessLogsText);
logsScroll.add(logsText);
logsStatusBox.add(logsStatusText);
accessLogsBox.add(accessLogsFilterBar);
accessLogsBox.add(accessLogsHeaderText);
accessLogsBox.add(accessLogsScroll);
accessLogsBox.add(accessLogsPageText);
accessLogDetailScroll.add(accessLogDetailText);
accessLogDetailBox.add(accessLogDetailScroll);
accessLogsRow.add(accessLogsBox);
accessLogsRow.add(accessLogDetailBox);
logsBox.add(logsScroll);
logsView.add(logsStatusBox);
logsView.add(accessLogsRow);

const configView = new BoxRenderable(renderer, {
  visible: false,
  flexGrow: 1,
  rowGap: 1,
});

const configSummaryBox = new BoxRenderable(renderer, {
  height: 6,
});

const configSummaryText = new TextRenderable(renderer, {
  content: "No source selected.",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
  height: "100%",
});

const caddyfileBlockBox = new BoxRenderable(renderer, {
  flexGrow: 1,
});

const caddyfileBlockScroll = new ScrollBoxRenderable(renderer, {
  width: "100%",
  height: "100%",
  flexGrow: 1,
  scrollY: true,
  scrollX: false,
});

const caddyfileBlockText = new TextRenderable(renderer, {
  content: "",
  fg: "#111827",
  wrapMode: "none",
  width: "100%",
});

const activeConfigBox = new BoxRenderable(renderer, {
  height: 10,
});

const activeConfigText = new TextRenderable(renderer, {
  content: "",
  fg: "#475569",
  wrapMode: "word",
  width: "100%",
  height: "100%",
});

const configScroll = new ScrollBoxRenderable(renderer, {
  width: "100%",
  height: "100%",
  flexGrow: 1,
  scrollY: true,
  scrollX: false,
});

const configOverlayText = new TextRenderable(renderer, {
  content: "",
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
});

caddyfileBlockScroll.add(caddyfileBlockText);
configSummaryBox.add(configSummaryText);
caddyfileBlockBox.add(caddyfileBlockScroll);
activeConfigBox.add(activeConfigText);
configScroll.add(configOverlayText);
configView.add(configScroll);
detailPanel.add(logsView);
detailPanel.add(configView);

const helpView = new BoxRenderable(renderer, {
  visible: false,
  flexGrow: 1,
});

const helpScroll = new ScrollBoxRenderable(renderer, {
  width: "100%",
  height: "100%",
  flexGrow: 1,
  scrollY: true,
  scrollX: false,
});

const helpText = new TextRenderable(renderer, {
  content: helpContent(),
  fg: "#111827",
  wrapMode: "word",
  width: "100%",
});

helpScroll.add(helpText);
helpView.add(helpScroll);

const footer = new BoxRenderable(renderer, {
  height: 1,
  width: "100%",
  flexDirection: "row",
  justifyContent: "space-between",
});

const footerText = new TextRenderable(renderer, {
  content: footerLine(),
  fg: "#475569",
});

footer.add(footerText);
footer.add(statusText);

root.add(tabBar);
root.add(body);
root.add(serviceView);
root.add(helpView);
root.add(footer);
renderer.root.add(root);
setActiveView("services");

renderer.on("resize", () => {
  renderServicesList();
  renderLogsView();
});

renderer.keyInput.on("keypress", (key) => {
  if (key.name === "q" && !key.ctrl && !key.meta) {
    renderer.destroy();
    return;
  }

  if (isBackKey(key) && (activeView === "help" || activeView === "system" || activeView === "logs" || activeView === "logDetail" || activeView === "config")) {
    key.preventDefault();
    key.stopPropagation();
    setActiveView(activeView === "help" ? previousHelpView : activeView === "system" ? previousMainView : activeView === "logs" ? previousView() : activeView === "logDetail" ? "logs" : previousConfigView);
    return;
  }

  if (isHelpShortcut(key) && !key.ctrl && !key.meta) {
    key.preventDefault();
    key.stopPropagation();
    toggleHelpView();
    return;
  }

  if (key.name === "r" && !key.ctrl && !key.meta) {
    void refreshAll();
    return;
  }

  if (isSystemShortcut(key) && !key.ctrl && !key.meta) {
    key.preventDefault();
    key.stopPropagation();
    toggleSystemView();
    return;
  }

  if (activeView === "system" && key.name === "v" && !key.ctrl && !key.meta) {
    void validateConfig();
    return;
  }

  if (activeView !== "system" && activeView !== "help" && activeView !== "logDetail" && activeView !== "config" && isBackKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    setActiveView(previousView());
    return;
  }

  if (activeView !== "system" && activeView !== "help" && activeView !== "logs" && activeView !== "logDetail" && activeView !== "config" && isForwardKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    setActiveView(nextView());
    return;
  }

  if (activeView === "services" && isDownKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    moveServiceSelection(1);
    return;
  }

  if (activeView === "services" && isUpKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    moveServiceSelection(-1);
    return;
  }

  if (activeView === "services" && isForwardKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    setActiveView("logs");
    return;
  }

  if (activeView === "logs" && isDownKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    moveAccessLogSelection(1);
    return;
  }

  if (activeView === "logs" && isUpKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    moveAccessLogSelection(-1);
    return;
  }

  if (activeView === "logs" && isForwardKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    if (visibleAccessLogEntries().length > 0) {
      accessLogDetailScroll.scrollTo(0);
      setActiveView("logDetail");
    }
    return;
  }

  if (activeView === "logDetail" && isDownKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    accessLogDetailScroll.scrollBy(1);
    return;
  }

  if (activeView === "logDetail" && isUpKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    accessLogDetailScroll.scrollBy(-1);
    return;
  }

  if (activeView === "system" && isDownKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    systemScroll.scrollBy(1);
    return;
  }

  if (activeView === "system" && isUpKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    systemScroll.scrollBy(-1);
    return;
  }

  if (activeView === "help" && isDownKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    helpScroll.scrollBy(1);
    return;
  }

  if (activeView === "help" && isUpKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    helpScroll.scrollBy(-1);
    return;
  }

  if ((activeView === "logs" || activeView === "logDetail" || activeView === "system") && key.name === "e" && !key.ctrl && !key.meta) {
    logsErrorOnly = !logsErrorOnly;
    renderLogsView();
    renderServiceView();
    return;
  }

  if ((activeView === "logs" || activeView === "logDetail") && key.name === "s" && !key.ctrl && !key.meta) {
    logsSlowOnly = !logsSlowOnly;
    renderLogsView();
    return;
  }

  if ((activeView === "logs" || activeView === "logDetail") && isLogsTimeWindowKey(key.name) && !key.ctrl && !key.meta) {
    logsTimeWindow = timeWindowForKey(key.name);
    selectedAccessLogIndex = 0;
    renderLogsView();
    return;
  }

  if ((activeView === "services" || activeView === "logs" || activeView === "logDetail" || activeView === "config") && key.name === "c" && !key.ctrl && !key.meta) {
    key.preventDefault();
    key.stopPropagation();
    toggleConfigView();
    return;
  }

  if (activeView === "config" && isDownKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    configScroll.scrollBy(1);
    return;
  }

  if (activeView === "config" && isUpKey(key)) {
    key.preventDefault();
    key.stopPropagation();
    configScroll.scrollBy(-1);
    return;
  }

  if (key.name === "tab" && !key.ctrl && !key.meta) {
    setActiveView(activeView === "system" ? previousMainView : activeView === "help" ? previousHelpView : activeView === "config" ? previousConfigView : nextView());
  }
});

function isHelpShortcut(key: { name: string; sequence: string }): boolean {
  return key.sequence === "?" || key.name === "?";
}

function isSystemShortcut(key: { name: string; sequence: string; shift: boolean }): boolean {
  return key.sequence === "S" || (key.name.toLowerCase() === "s" && key.shift);
}

function isBackKey(key: { name: string; ctrl: boolean; meta: boolean }): boolean {
  return !key.ctrl && !key.meta && (key.name === "escape" || key.name === "h" || key.name === "left");
}

function isForwardKey(key: { name: string; ctrl: boolean; meta: boolean }): boolean {
  return !key.ctrl && !key.meta && (key.name === "return" || key.name === "enter" || key.name === "l" || key.name === "right");
}

function isDownKey(key: { name: string; ctrl: boolean; meta: boolean }): boolean {
  return !key.ctrl && !key.meta && (key.name === "j" || key.name === "down");
}

function isUpKey(key: { name: string; ctrl: boolean; meta: boolean }): boolean {
  return !key.ctrl && !key.meta && (key.name === "k" || key.name === "up");
}

function isLogsTimeWindowKey(name: string): boolean {
  return name === "d" || name === "w" || name === "a";
}

function timeWindowForKey(name: string): LogsTimeWindow {
  if (name === "w") return "week";
  if (name === "a") return "all";

  return "day";
}

function openSystemView(): void {
  if (isMainView(activeView)) previousMainView = activeView;
  if (activeView !== "help") previousHelpView = "system";
  setActiveView("system");
}

function toggleSystemView(): void {
  if (activeView === "system") {
    setActiveView(previousMainView);
    return;
  }

  openSystemView();
}

function toggleConfigView(): void {
  if (activeView === "config") {
    setActiveView(previousConfigView);
    return;
  }

  if (activeView === "services" || activeView === "logs" || activeView === "logDetail") previousConfigView = activeView;
  setActiveView("config");
}

function toggleHelpView(): void {
  if (activeView === "help") {
    setActiveView(previousHelpView);
    return;
  }

  previousHelpView = activeView;
  setActiveView("help");
}

function isMainView(view: ActiveView): view is MainView {
  return ["services", "logs", "logDetail"].includes(view);
}

void refreshAll();

async function refreshAll(): Promise<void> {
  const selectedSourceId = selectedSource()?.id;
  const selectedRequestKey = selectedAccessLogEntryKey();
  const selectedRequestIndex = selectedAccessLogIndex;

  await refreshFromAdmin(selectedSourceId);
  await Promise.all([refreshLogs({ selectedRequestKey, selectedRequestIndex }), refreshSelectedUpstreamHealth()]);
}

async function validateConfig(): Promise<void> {
  if (validationInFlight) return validationInFlight;

  validationInFlight = (async () => {
    validating = true;
    renderServiceView();

    lastValidation = await validateCaddyConfig(
      discovery.configPath || discovery.command?.configPath,
      discovery.adapter || discovery.command?.adapter,
    );

    validating = false;
    renderServiceView();
  })().finally(() => {
    validationInFlight = undefined;
  });

  return validationInFlight;
}

async function refreshSelectedUpstreamHealth(): Promise<void> {
  const source = selectedSource();
  if (!source) {
    renderDetail(undefined);
    return;
  }

  if (upstreamHealthInFlight && healthCheckingSourceId === source.id) return upstreamHealthInFlight;

  const healthPromise = (async () => {
    healthCheckingSourceId = source.id;
    renderServicesList();
    renderDetail(source);

    const results = await checkSourceUpstreams(source);
    upstreamHealthBySource.set(source.id, results);

    if (healthCheckingSourceId === source.id) {
      healthCheckingSourceId = undefined;
    }

    if (!root.isDestroyed && selectedSource()?.id === source.id) {
      renderServicesList();
      renderDetail(source);
    }
  })().finally(() => {
    if (upstreamHealthInFlight === healthPromise) {
      upstreamHealthInFlight = undefined;
    }
  });

  upstreamHealthInFlight = healthPromise;
  return healthPromise;
}

async function refreshAccessLogs(options: { preserveSelection?: boolean } = {}): Promise<void> {
  if (root.isDestroyed) return;

  const previousKey = options.preserveSelection ? selectedAccessLogEntryKey() : undefined;
  const previousIndex = options.preserveSelection ? selectedAccessLogIndex : -1;
  const source = selectedSource();
  lastAccessLogs = await fetchAccessLogsForSource(source, lastLogs, 5000);
  updateAccessLogStats(source, lastAccessLogs);
  restoreAccessLogSelection(previousKey, previousIndex);
  renderServicesList();
  renderLogsView();
}

async function refreshAccessLogsForAllSources(): Promise<void> {
  const selected = selectedSource();
  const results = await Promise.all(
    sources.map(async (source) => ({
      source,
      logs: await fetchAccessLogsForSource(source, lastLogs, 5000),
    })),
  );

  for (const { source, logs } of results) {
    updateAccessLogStats(source, logs);
    if (source.id === selected?.id) lastAccessLogs = logs;
  }

  if (!selected) lastAccessLogs = undefined;
}

function updateAccessLogStats(source: CaddySource | undefined, logs: CaddyAccessLogsResult | undefined): void {
  if (!source || !logs) return;

  accessLogStatsBySource.set(source.id, {
    available: logs.available,
    ok: logs.ok,
    count24h: countAccessLogsInLast24Hours(logs),
  });
}

function countAccessLogsInLast24Hours(logs: CaddyAccessLogsResult): number | undefined {
  if (!logs.available || !logs.ok) return undefined;

  const since = Date.now() - 24 * 60 * 60 * 1000;
  const parsed = parseAccessLogOutput(logs.output).filter((entry) => entry.parsed);
  if (parsed.length === 0) return 0;

  const timestamped = parsed.filter((entry) => entry.timestamp);
  if (timestamped.length === 0) return undefined;

  return timestamped.filter((entry) => entry.timestamp && entry.timestamp.getTime() >= since).length;
}

function moveAccessLogSelection(delta: number): void {
  const entries = visibleAccessLogEntries();
  if (entries.length === 0) return;

  selectedAccessLogIndex = clamp(selectedAccessLogIndex < 0 ? 0 : selectedAccessLogIndex + delta, 0, entries.length - 1);
  accessLogsScroll.scrollTo(0);
  renderLogsView();
}

function clampSelectedAccessLogIndex(entries: ParsedAccessLogEntry[]): void {
  if (entries.length === 0) {
    selectedAccessLogIndex = -1;
    return;
  }

  if (selectedAccessLogIndex < 0) {
    selectedAccessLogIndex = 0;
    return;
  }

  selectedAccessLogIndex = clamp(selectedAccessLogIndex, 0, entries.length - 1);
}

function selectedAccessLogEntryKey(): string | undefined {
  const entries = visibleAccessLogEntries();
  const entry = entries[selectedAccessLogIndex];

  return entry ? accessLogEntryKey(entry) : undefined;
}

function restoreAccessLogSelection(previousKey: string | undefined, previousIndex: number): void {
  const entries = visibleAccessLogEntries();
  if (entries.length === 0) {
    selectedAccessLogIndex = -1;
    return;
  }

  if (previousKey) {
    const index = entries.findIndex((entry) => accessLogEntryKey(entry) === previousKey);
    if (index !== -1) {
      selectedAccessLogIndex = index;
      return;
    }
  }

  selectedAccessLogIndex = previousIndex >= 0 ? clamp(previousIndex, 0, entries.length - 1) : 0;
}

function accessLogEntryKey(entry: ParsedAccessLogEntry): string {
  return [
    entry.timestamp?.toISOString() || "",
    entry.method || "",
    entry.uri || "",
    entry.status ?? "",
    entry.durationMs ?? "",
    entry.raw,
  ].join("\u0000");
}

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value));
}

async function refreshLogs(options: { selectedRequestKey?: string; selectedRequestIndex?: number } = {}): Promise<void> {
  if (logsRefreshInFlight) return logsRefreshInFlight;

  logsRefreshInFlight = (async () => {
    const previousKey = options.selectedRequestKey ?? selectedAccessLogEntryKey();
    const previousIndex = options.selectedRequestIndex ?? selectedAccessLogIndex;
    logsRefreshing = true;
    renderLogsView();

    lastLogs = await fetchCaddyLogs();
    await refreshAccessLogsForAllSources();
    restoreAccessLogSelection(previousKey, previousIndex);

    logsRefreshing = false;
    renderServicesList();
    renderLogsView();
  })().finally(() => {
    logsRefreshInFlight = undefined;
  });

  return logsRefreshInFlight;
}

async function refreshFromAdmin(preferredSourceId = selectedSource()?.id): Promise<void> {
  if (refreshInFlight) return refreshInFlight;

  const fallbackIndex = selectedIndex;

  refreshInFlight = (async () => {
    refreshing = true;
    updateRefreshingUi();

    if (!adminUrl) {
      lastLoad = {
        adminUrl: "(not discovered)",
        endpoint: "(admin API disabled or unsupported)",
        ok: false,
        error: adminUnavailableMessage(),
        fetchedAt: new Date(),
        durationMs: 0,
      };
      sources = [];
      selectedIndex = -1;
      refreshing = false;
      updateUi();
      return;
    }

    const result = await fetchActiveCaddyConfig(adminUrl);
    lastLoad = result;
    sources = result.config ? extractCaddySources(result.config) : [];
    selectedIndex = restoredSelectedSourceIndex(preferredSourceId, fallbackIndex);

    refreshing = false;
    updateUi();
  })().finally(() => {
    refreshInFlight = undefined;
  });

  return refreshInFlight;
}

function setActiveView(view: ActiveView): void {
  if (isMainView(view) && view !== "logDetail") previousMainView = view;

  activeView = view;
  body.visible = isMainView(view) || view === "config";
  serviceView.visible = view === "system";
  helpView.visible = view === "help";
  renderSourceDetailLayout();
  tabBar.visible = view !== "services";
  tabText.content = tabsLine();
  sourceTabText.content = sourceTabsLine();
  footerText.content = footerLine();
  helpText.content = helpContent();
  renderDetail(selectedSource());
  renderLogsView();
  renderConfigView();
  renderServiceView();

  if (view === "services") {
    servicesScroll.focus();
  } else if (view === "logs") {
    accessLogsScroll.focus();
  } else if (view === "logDetail") {
    accessLogDetailScroll.focus();
  } else if (view === "config") {
    configScroll.focus();
  } else if (view === "system") {
    systemScroll.focus();
  } else if (view === "help") {
    helpScroll.focus();
  }
}

function renderSourceDetailLayout(): void {
  const showSourceDetail = (isMainView(activeView) && activeView !== "services") || activeView === "config";

  proxyPanel.visible = activeView === "services";
  detailPanel.visible = showSourceDetail;
  sourceTabBox.visible = false;
  sourceBox.visible = false;
  routesBox.visible = false;
  reverseProxyBox.visible = false;
  upstreamHealthBox.visible = false;
  configBox.visible = false;
  logsView.visible = activeView === "logs" || activeView === "logDetail";
  accessLogsBox.visible = activeView === "logs";
  accessLogsHeaderText.visible = activeView === "logs";
  accessLogDetailBox.visible = activeView === "logDetail";
  configView.visible = activeView === "config";

  sourceBox.height = 8;
  routesBox.flexGrow = 1;
  reverseProxyBox.flexGrow = undefined;
  reverseProxyBox.height = 6;
  upstreamHealthBox.height = 6;
}

function nextView(): ActiveView {
  if (activeView === "services") return "logs";

  return "logs";
}

function previousView(): ActiveView {
  if (activeView === "services") return "services";

  return "services";
}

function tabsLine(): string | StyledText {
  if (activeView === "system") return systemBreadcrumbLine();
  if (activeView === "help") return "Shortcuts";
  if (activeView === "services") return "Services";
  if (activeView === "config") return configBreadcrumbLine();
  if (activeView === "logs" || activeView === "logDetail") return logsBreadcrumbLine();

  return currentServiceLabel();
}

function systemBreadcrumbLine(): StyledText {
  return styledLines([[
    breadcrumbSegment("caddy", "context"),
    breadcrumbSeparator(),
    breadcrumbSegment("system", "active"),
  ]]);
}

function configBreadcrumbLine(): StyledText {
  const serviceLabel = breadcrumbServiceLabel();

  return styledLines([[
    breadcrumbSegment(serviceLabel, "context"),
    breadcrumbSeparator(),
    breadcrumbSegment("config", "active"),
  ]]);
}

function logsBreadcrumbLine(): StyledText {
  const serviceLabel = breadcrumbServiceLabel();
  const chunks: TextChunk[] = [
    breadcrumbSegment(serviceLabel, "context"),
    breadcrumbSeparator(),
    breadcrumbSegment("logs", activeView === "logs" ? "active" : "inactive"),
  ];

  if (activeView === "logDetail") {
    chunks.push(breadcrumbSeparator(), breadcrumbSegment(selectedRequestBreadcrumbLabel(serviceLabel.length), "active"));
  }

  return styledLines([chunks]);
}

function breadcrumbSegment(label: string, state: "inactive" | "context" | "active"): TextChunk {
  if (state === "active") return bold(fg("#2563eb")(label));
  if (state === "context") return bold(normalChunk(label));

  return mutedChunk(label);
}

function breadcrumbServiceLabel(): string {
  const maxLength = Math.min(36, Math.max(16, Math.floor(contentWidth() * 0.34)));

  return truncateMiddleOneLine(currentServiceLabel(), maxLength);
}

function selectedRequestBreadcrumbLabel(serviceLabelLength: number): string {
  const entries = visibleAccessLogEntries();
  clampSelectedAccessLogIndex(entries);
  const entry = entries[selectedAccessLogIndex];
  if (!entry) return "request";

  const raw = [
    entry.status ?? "-",
    entry.method || "-",
    accessLogPath(entry.uri) || entry.uri || entry.message || "-",
  ].join(" ");
  const maxLength = Math.max(16, contentWidth() - serviceLabelLength - 24);

  return truncateOneLine(raw, maxLength);
}

function breadcrumbSeparator(): TextChunk {
  return mutedChunk(" › ");
}

function currentServiceLabel(): string {
  const source = selectedSource() || sources[0];
  return source ? sourceLabel(source) : "Service";
}

function sourceTabsLine(): string | StyledText {
  return tabsLine();
}

function footerLine(): string {
  const base = "? help  S system  r refresh  q quit";

  if (activeView === "services") return `→ logs  c config  ${base}`;
  if (activeView === "logs") return `← back  → detail  ${base}`;
  if (activeView === "logDetail") return `← back  ${base}`;
  if (activeView === "config") return `←/c back  ${base}`;
  if (activeView === "system") return `←/S return  ? help  e errors  r refresh  v validate  q quit`;
  if (activeView === "help") return `←/? return  S system  r refresh  q quit`;

  return base;
}

function helpContent(): string {
  return [
    "Shortcuts",
    "",
    "Global",
    "  ?        show/return from this help screen",
    "  ←/esc/h  go back from Logs/System/Config/request detail/help",
    "  S        open/return from System",
    "  q        quit",
    "  r        refresh active config, logs, and upstream health",
    "",
    "Services",
    "  ↓/j      next service",
    "  ↑/k      previous service",
    "  →/enter/l open selected service Logs",
    "  c        open Config for selected service",
    "",
    "Service detail",
    "  ←/esc/h  from Logs returns to Services",
    "  →/enter/l from Services enters Logs",
    "  tab      return from System/Help/Config",
    "  c        open/close Config from Services, Logs, or request detail",
    "  sections Logs",
    "",
    "Logs",
    "  ↓/j      next access log entry",
    "  ↑/k      previous access log entry",
    "  →/enter/l open selected request detail",
    "  ←/esc/h  close request detail / return to Services from Logs",
    "  s        toggle slow requests filter",
    "  e        toggle errors/warnings-only filter",
    "  d/w/a    time window: day/week/all",
    "",
    "Config",
    "  ↓/j      scroll config overlay down",
    "  ↑/k      scroll config overlay up",
    "  ←/esc/h  close Config",
    "",
    "System",
    "  v        validate Caddy config",
    "  ↓/j      scroll system overlay down",
    "  ↑/k      scroll system overlay up",
    "  ←/esc/h  return",
    "  e        toggle errors/warnings-only service logs",
  ].join("\n");
}

function updateRefreshingUi(): void {
  if (root.isDestroyed) return;

  updateHeaderStatus();
  footerText.content = `refreshing...  ${footerLine()}`;

  if (sources.length === 0) {
    renderServicesList();
    renderLoadingDetail();
  }

  renderServiceView();
}

function updateUi(): void {
  if (root.isDestroyed) return;

  updateHeaderStatus();
  footerText.content = refreshing ? `refreshing...  ${footerLine()}` : footerLine();

  renderServicesList();
  updateDetail();
  renderServiceView();
  renderLogsView();
  renderConfigView();
}

function updateDetail(): void {
  if (root.isDestroyed) return;

  const source = selectedSource();

  renderSourceDetailLayout();
  renderDetail(source);
  renderLogsView();
  renderConfigView();
}

function selectedSource(): CaddySource | undefined {
  if (selectedIndex < 0 || selectedIndex >= sources.length) return undefined;

  return sources[selectedIndex];
}

function restoredSelectedSourceIndex(preferredSourceId: string | undefined, fallbackIndex: number): number {
  if (sources.length === 0) return -1;

  if (preferredSourceId) {
    const index = sources.findIndex((source) => source.id === preferredSourceId);
    if (index !== -1) return index;
  }

  return clamp(fallbackIndex, 0, sources.length - 1);
}

function moveServiceSelection(delta: number): void {
  if (sources.length === 0) return;

  selectedIndex = clamp(selectedIndex < 0 ? 0 : selectedIndex + delta, 0, sources.length - 1);
  renderServicesList();
  updateDetail();
  void refreshAccessLogs({ preserveSelection: false });
  void refreshSelectedUpstreamHealth();
}

function renderServicesList(): void {
  if (root.isDestroyed) return;

  servicesText.content = servicesListContent();
  servicesScroll.scrollTo(Math.max(0, selectedIndex * 2 - 1));
}

function servicesListContent(): StyledText {
  if (refreshing && sources.length === 0) {
    return styledLines([
      [normalChunk("Loading active config")],
      [mutedChunk(adminUrl ? `GET ${adminUrl}/config/ from the running Caddy Admin API` : "No queryable Admin API endpoint discovered")],
    ]);
  }

  if (!lastLoad.ok) {
    return styledLines([
      [errorChunk("Admin API unavailable")],
      [mutedChunk(adminUrl ? "Caddy is not reachable yet; press r to retry" : "No queryable endpoint discovered; use --admin-url to override")],
    ]);
  }

  if (sources.length === 0) {
    return styledLines([
      [normalChunk("No proxied sources found")],
      [mutedChunk("Active config loaded, but no sources with reverse_proxy routes were detected")],
    ]);
  }

  const lines: TextChunk[][] = [];

  for (const [index, source] of sources.entries()) {
    const selected = index === selectedIndex;
    lines.push(serviceListLine([
      bold(normalChunk(serviceCardTitle(source))),
    ], selected));
    lines.push(serviceListLine([
      mutedChunk("≡ "),
      serviceLogsChunk(source),
      mutedChunk("  ⇄ "),
      mutedChunk(serviceRouteCountLabel(source)),
      mutedChunk("  "),
      serviceReachabilityChunk(source),
      normalChunk(" "),
      normalChunk(serviceTargetSummary(source)),
    ], selected));
  }

  return styledLines(lines);
}

function styledLines(lines: TextChunk[][]): StyledText {
  const chunks: TextChunk[] = [];

  lines.forEach((line, index) => {
    chunks.push(...line);
    if (index < lines.length - 1) chunks.push(normalChunk("\n"));
  });

  return new StyledText(chunks);
}

function normalChunk(value: string | number | boolean): TextChunk {
  return fg("#111827")(value);
}

function mutedChunk(value: string | number | boolean): TextChunk {
  return fg("#64748b")(value);
}

function serviceListLine(chunks: TextChunk[], selected: boolean): TextChunk[] {
  if (!selected) return chunks;

  const width = contentWidth();
  const textLength = chunks.reduce((total, chunk) => total + chunk.text.length, 0);
  const padding = Math.max(0, width - textLength);

  return [
    ...chunks.map((chunk) => bg("#dbeafe")(chunk)),
    bg("#dbeafe")(" ".repeat(padding)),
  ];
}

function errorChunk(value: string | number | boolean): TextChunk {
  return fg("#dc2626")(value);
}

function successChunk(value: string | number | boolean): TextChunk {
  return fg("#16a34a")(value);
}

function warningChunk(value: string | number | boolean): TextChunk {
  return fg("#d97706")(value);
}

function sectionTitleChunk(value: string): TextChunk {
  return bold(fg("#2563eb")(value));
}

function serviceReachabilityChunk(source: CaddySource): TextChunk {
  const status = serviceReachabilityStatus(source);
  if (status === "REACHABLE") return successChunk("●");
  if (status === "UNREACHABLE") return errorChunk("●");
  if (status === "CHECKING") return warningChunk("●");

  return mutedChunk("●");
}

function serviceLogsChunk(source: CaddySource): TextChunk {
  const stats = accessLogStatsBySource.get(source.id);
  const disabled = source.accessLogs.length === 0 || stats?.available === false;

  return disabled ? mutedChunk(serviceLogsLabel(source)) : normalChunk(serviceLogsLabel(source));
}

function serviceLogsLabel(source: CaddySource): string {
  const stats = accessLogStatsBySource.get(source.id);
  const disabled = source.accessLogs.length === 0 || stats?.available === false;
  const label = disabled
    ? "logs off"
    : stats?.count24h === undefined
      ? "- reqs"
      : formatRequestCountLabel(stats.count24h);

  return label.padEnd("99999 reqs".length);
}

function formatRequestCountLabel(count: number): string {
  if (count >= 100_000) return `${Math.round(count / 1000)}k reqs`;

  return `${count} req${count === 1 ? "" : "s"}`;
}

function serviceRouteCountLabel(source: CaddySource): string {
  return `${source.routes.length} route${source.routes.length === 1 ? "" : "s"}`.padEnd("99 routes".length);
}

function updateHeaderStatus(): void {
  statusText.content = "●";
  statusText.fg = refreshing ? "#d97706" : lastLoad.ok ? "#16a34a" : "#dc2626";
  titleText.content = "lazycaddy";
}

function renderLoadingDetail(): void {
  if (root.isDestroyed) return;


  sourceSectionText.content = adminUrl
    ? "Fetching active Caddy config from the running Admin API..."
    : adminUnavailableMessage();
  routesText.content = "";
  reverseProxyText.content = "";
  upstreamHealthText.content = "";
  configText.content = formatConfigSectionLines().join("\n");
}

function renderDetail(source: CaddySource | undefined): void {
  if (root.isDestroyed) return;

  sourceTabText.content = sourceTabsLine();
  tabText.content = tabsLine();

  if (!lastLoad.ok) {

    sourceSectionText.content = [
      "Could not load the active Caddy config.",
      lastLoad.error || "Unknown Admin API error",
    ].join("\n");
    routesText.content = formatConfigSectionLines().join("\n");
    reverseProxyText.content = [
      "CADDY_ADMIN_API=http://localhost:2019 bun dev",
      "bun dev --admin-url http://localhost:2019",
    ].join("\n");
    upstreamHealthText.content = "Load active config before checking upstreams.";
    configText.content = "lazycaddy uses the running Caddy Admin API as the source of truth.";
    return;
  }

  if (!source) {
    sourceSectionText.content = "Active Caddy config loaded successfully.";
    routesText.content = "No sources with reverse_proxy routes were found.";
    reverseProxyText.content = "(none)";
    upstreamHealthText.content = "(none)";
    configText.content = formatConfigSectionLines().join("\n");
    return;
  }

  sourceSectionText.content = "";
}

function renderServiceView(): void {
  if (root.isDestroyed) return;

  systemText.content = systemContent();
}

function renderLogsView(): void {
  if (root.isDestroyed) return;

  const logsStatusLines = formatLogsStatusLines();
  logsStatusBox.visible = logsStatusLines.length > 0;
  logsStatusText.content = logsStatusLines.join("\n");
  logsStatusText.fg = lastAccessLogs?.available === false ? "#d97706" : lastAccessLogs?.ok === false ? "#dc2626" : logsRefreshing ? "#d97706" : "#111827";
  accessLogsTimeFilterText.content = accessLogsTimeFilterContent();
  accessLogsKindFilterText.content = accessLogsKindFilterContent();
  accessLogsHeaderText.content = accessLogsHeaderContent();
  accessLogsText.content = formatAccessLogsContent();
  accessLogsPageText.content = accessLogsPageContent();
  accessLogsText.fg = lastAccessLogs?.available === false ? "#d97706" : lastAccessLogs?.ok === false ? "#dc2626" : "#111827";
  accessLogDetailText.content = formatAccessLogDetailContent();
  accessLogDetailText.fg = lastAccessLogs?.available === false ? "#d97706" : "#111827";
}

function renderConfigView(): void {
  if (root.isDestroyed) return;

  const source = selectedSource();
  const block = findCaddyfileSource(caddyfileCorrelation, source);
  configOverlayText.content = configContent(source, block);
}

function formatLogsStatusLines(): string[] {
  return [];
}

function formatAccessLogsContent(): string | StyledText {
  if (logsRefreshing && !lastAccessLogs) return "Checking selected source access logs...";
  if (!lastAccessLogs) return "No source access logs loaded yet.";
  if (!lastAccessLogs.available) return `Access logs off for ${selectedSourceLabel()}.`;
  if (!lastAccessLogs.ok && !lastAccessLogs.output) return lastAccessLogs.error || "Access log read error.";

  const entries = visibleAccessLogEntries();

  if (entries.length === 0) {
    return logsErrorOnly ? "No error/warning requests found." : "No parsed requests found.";
  }

  clampSelectedAccessLogIndex(entries);
  const window = accessLogWindow(entries);

  return styledLines(window.entries.map((entry, index) => requestTableRowChunks(entry, window.start + index === selectedAccessLogIndex)));
}

function accessLogWindow(entries: ParsedAccessLogEntry[]): { start: number; end: number; entries: ParsedAccessLogEntry[] } {
  const selected = clamp(selectedAccessLogIndex < 0 ? 0 : selectedAccessLogIndex, 0, entries.length - 1);
  const visibleRows = accessLogVisibleRows();
  const before = Math.min(accessLogWindowBefore, Math.max(1, Math.floor((visibleRows - 1) / 3)));
  const after = Math.min(accessLogWindowAfter, Math.max(1, visibleRows - before - 1));
  let start = Math.max(0, selected - before);
  let end = Math.min(entries.length, selected + after + 1);

  if (end - start < visibleRows) {
    start = Math.max(0, Math.min(start, end - visibleRows));
    end = Math.min(entries.length, Math.max(end, start + visibleRows));
  }

  return { start, end, entries: entries.slice(start, end) };
}

function accessLogVisibleRows(): number {
  // Header + tabs + footer + logs status + filter row + table header + root padding/gaps take roughly 10 rows.
  return Math.max(minAccessLogWindowRows, (renderer.height || process.stdout.rows || 24) - 10);
}

function accessLogsPageContent(): string {
  if (!lastAccessLogs?.available || !lastAccessLogs.ok) return "";

  const entries = visibleAccessLogEntries();
  if (entries.length === 0) return "0/0";

  clampSelectedAccessLogIndex(entries);
  const window = accessLogWindow(entries);

  return `${window.start + 1}-${window.end} of ${entries.length} · ${accessLogTotalsSummary()}`;
}

function accessLogTotalsSummary(): string {
  const slow = accessLogEntriesForTimeWindow(parsedAccessLogEntries()).filter(isSlowAccessLogEntry).length;

  return `${slow} slow`;
}

function accessLogsTimeFilterContent(): StyledText {
  return styledLines([[
    ...filterLabelChunks("day", "d", logsTimeWindow === "day"),
    mutedChunk("  "),
    ...filterLabelChunks("week", "w", logsTimeWindow === "week"),
    mutedChunk("  "),
    ...filterLabelChunks("all", "a", logsTimeWindow === "all"),
  ]]);
}

function accessLogsKindFilterContent(): StyledText {
  return styledLines([[
    ...filterLabelChunks("slow", "s", logsSlowOnly),
    mutedChunk("  "),
    ...filterLabelChunks("error", "e", logsErrorOnly),
  ]]);
}

function filterLabelChunks(label: string, key: string, active: boolean): TextChunk[] {
  const chunks = [
    active ? warningChunk("● ") : mutedChunk("○ "),
    warningChunk(key),
    active ? normalChunk(label.slice(1)) : mutedChunk(label.slice(1)),
  ];

  return active ? chunks.map((chunk) => bold(chunk)) : chunks;
}

function accessLogsHeaderContent(): string {
  if (!lastAccessLogs?.available || !lastAccessLogs.ok || visibleAccessLogEntries().length === 0) return "";

  return requestTableHeader();
}

function formatAccessLogDetailContent(): string | StyledText {
  if (logsRefreshing && !lastAccessLogs) return "Waiting for access logs...";
  if (!lastAccessLogs) return "No access log entry selected.";
  if (!lastAccessLogs.available) return accessLogHelper(selectedSource());

  const entries = visibleAccessLogEntries();
  clampSelectedAccessLogIndex(entries);
  const entry = entries[selectedAccessLogIndex];
  if (!entry) return "No access log entry selected.";

  return accessLogDetailStyledContent(entry);
}

function accessLogDetailStyledContent(entry: ParsedAccessLogEntry): StyledText {
  const lines: TextChunk[][] = [];
  const path = accessLogPath(entry.uri) || entry.uri || entry.message || "-";
  const title = `${entry.method || "-"} ${path}`;

  lines.push(...wrapText(title, contentWidth()).map((line) => [bold(normalChunk(line))]));
  lines.push([
    statusCodeChunk(entry.status, String(entry.status ?? "-").padEnd(3).slice(0, 3)),
    mutedChunk("  "),
    normalChunk(formatRequestLatency(entry.durationMs)),
    mutedChunk("  "),
    normalChunk(formatSizeBytes(entry.sizeBytes)),
    mutedChunk("  "),
    mutedChunk(formatRequestTime(entry.timestamp)),
  ]);
  lines.push([]);

  pushDetailSection(lines, "Matched route", formatMatchedRouteLines(selectedSource(), entry).map((line) => [normalChunk(line)]));
  pushDetailSection(lines, "Request", compactDetailRows([
    detailFieldLine("Host", entry.host),
    detailFieldLine("Method", entry.method),
    detailFieldLine("URI", entry.uri),
    detailFieldLine("Remote", remoteAddressLabel(entry)),
    detailFieldLine("Client IP", entry.clientIp),
    detailFieldLine("Protocol", entry.protocol),
    detailFieldLine("Agent", entry.userAgent),
    detailFieldLine("Referer", entry.referer),
  ]));
  pushDetailSection(lines, "Response", compactDetailRows([
    detailFieldLine("Status", entry.status === undefined ? undefined : String(entry.status), statusCodeChunk(entry.status, String(entry.status))),
    detailFieldLine("Duration", formatRequestLatency(entry.durationMs)),
    detailFieldLine("Size", formatSizeBytes(entry.sizeBytes)),
    detailFieldLine("Error", entry.error, entry.error ? errorChunk(entry.error) : undefined),
  ]));

  const headerLines = [
    ...detailHeaderLines("Request headers", entry.requestHeaders, ["User-Agent", "Accept", "Content-Type", "Content-Length", "X-Forwarded-For", "X-Real-IP"]),
    ...detailHeaderLines("Response headers", entry.responseHeaders, ["Content-Type", "Content-Length", "Location", "Server", "Cache-Control"]),
  ];
  if (headerLines.length > 0) pushDetailSection(lines, "Headers", headerLines);

  pushDetailSection(lines, "Metadata", compactDetailRows([
    detailFieldLine("Logger", entry.logger),
    detailFieldLine("Level", entry.level),
    detailFieldLine("Message", entry.message),
    detailFieldLine("TLS", tlsSummary(entry)),
  ]));

  if (entry.raw) {
    pushDetailSection(lines, "Raw", wrapText(entry.raw, Math.max(24, contentWidth() - 2)).map((line) => [mutedChunk(line)]));
  }

  return styledLines(lines);
}

function requestTableHeader(): string {
  return [
    "TIME".padEnd(8),
    "ST".padEnd(4),
    "METHOD".padEnd(6),
    "PATH".padEnd(requestTablePathWidth()),
    "LATENCY".padStart(8),
  ].join(" ");
}

function requestTableRowChunks(entry: ParsedAccessLogEntry, selected: boolean): TextChunk[] {
  const chunks = requestTableRowBaseChunks(entry);
  if (!selected) return chunks;

  const rowLength = chunks.reduce((total, chunk) => total + chunk.text.length, 0);
  const padding = Math.max(0, contentWidth() - rowLength);

  return [...chunks.map((chunk) => bg("#dbeafe")(chunk)), bg("#dbeafe")(" ".repeat(padding))];
}

function requestTableRowBaseChunks(entry: ParsedAccessLogEntry): TextChunk[] {
  const time = formatRequestTime(entry.timestamp).padEnd(8);
  const status = String(entry.status ?? "-").padEnd(4).slice(0, 4);
  const method = (entry.method || "-").padEnd(6).slice(0, 6);
  const pathWidth = requestTablePathWidth();
  const path = truncateOneLine(accessLogPath(entry.uri) || entry.uri || entry.message || "-", pathWidth).padEnd(pathWidth);
  const latency = formatRequestLatency(entry.durationMs).padStart(8);

  return [
    normalChunk(time),
    mutedChunk(" "),
    statusCodeChunk(entry.status, status),
    mutedChunk(" "),
    normalChunk(method),
    mutedChunk(" "),
    normalChunk(path),
    mutedChunk(" "),
    normalChunk(latency),
  ];
}

function statusCodeChunk(status: number | undefined, value: string): TextChunk {
  if (status === undefined) return mutedChunk(value);
  if (status >= 500) return errorChunk(value);
  if (status >= 400) return warningChunk(value);
  if (status >= 300) return fg("#2563eb")(value);
  if (status >= 200) return successChunk(value);

  return mutedChunk(value);
}

function pushDetailSection(lines: TextChunk[][], title: string, rows: TextChunk[][]): void {
  if (rows.length === 0) return;
  if (lines.length > 0 && lines.at(-1)?.length !== 0) lines.push([]);
  lines.push([bold(fg("#2563eb")(title))]);
  lines.push(...rows);
}

function compactDetailRows(rows: Array<TextChunk[] | undefined>): TextChunk[][] {
  return rows.filter((row): row is TextChunk[] => Boolean(row));
}

function detailFieldLine(label: string, value: string | number | undefined, valueChunk?: TextChunk): TextChunk[] | undefined {
  if (value === undefined || value === "") return undefined;

  const continuationIndent = " ".repeat(12);
  const wrappedValue = wrapText(String(value), Math.max(16, contentWidth() - continuationIndent.length)).join(`\n${continuationIndent}`);
  const chunk = valueChunk ? { ...valueChunk, text: wrappedValue } : normalChunk(wrappedValue);

  return [
    mutedChunk(label.padEnd(12)),
    chunk,
  ];
}

function detailHeaderLines(title: string, headers: Record<string, string[]> | undefined, preferred: string[]): TextChunk[][] {
  if (!headers) return [];
  const rows: TextChunk[][] = [];
  const lowerCaseHeaders = new Map(Object.entries(headers).map(([key, value]) => [key.toLowerCase(), [key, value] as const]));

  for (const name of preferred) {
    const header = lowerCaseHeaders.get(name.toLowerCase());
    if (!header) continue;

    rows.push(detailFieldLine(name, header[1].join(", "))!);
  }

  return rows.length > 0 ? [[mutedChunk(`${title}:`)], ...rows] : [];
}

function remoteAddressLabel(entry: ParsedAccessLogEntry): string | undefined {
  if (!entry.remoteIp) return undefined;

  return entry.remotePort ? `${entry.remoteIp}:${entry.remotePort}` : entry.remoteIp;
}

function tlsSummary(entry: ParsedAccessLogEntry): string | undefined {
  const parts = [entry.tls?.version, entry.tls?.protocol, entry.tls?.serverName].filter(Boolean);

  return parts.length > 0 ? parts.join(" · ") : undefined;
}

function formatSizeBytes(bytes: number | undefined): string {
  if (bytes === undefined) return "-";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;

  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

function requestTablePathWidth(): number {
  return Math.max(16, contentWidth() - 30);
}

function contentWidth(): number {
  return Math.max(40, (renderer.width || process.stdout.columns || 100) - 2);
}

function accessLogEntriesForTimeWindow(entries: ParsedAccessLogEntry[]): ParsedAccessLogEntry[] {
  switch (logsTimeWindow) {
    case "day":
      return accessLogEntriesSince(entries, 24 * 60 * 60 * 1000);
    case "week":
      return accessLogEntriesSince(entries, 7 * 24 * 60 * 60 * 1000);
    case "all":
      return entries;
  }
}

function accessLogEntriesSince(entries: ParsedAccessLogEntry[], durationMs: number): ParsedAccessLogEntry[] {
  const since = Date.now() - durationMs;
  const timestamped = entries.filter((entry) => entry.timestamp);

  return timestamped.length > 0
    ? timestamped.filter((entry) => entry.timestamp && entry.timestamp.getTime() >= since)
    : entries;
}

function isSlowAccessLogEntry(entry: ParsedAccessLogEntry): boolean {
  return entry.durationMs !== undefined && entry.durationMs >= slowRequestThresholdMs;
}

function formatRequestTime(date: Date | undefined): string {
  return date ? date.toLocaleTimeString(undefined, { hour12: false }).slice(0, 8) : "-";
}

function formatRequestLatency(durationMs: number | undefined): string {
  if (durationMs === undefined) return "-";
  if (durationMs < 1000) return `${Math.round(durationMs)}ms`;

  return `${(durationMs / 1000).toFixed(2)}s`;
}

function selectedSourceLabel(): string {
  const source = selectedSource();
  return source ? sourceLabel(source) : "selected source";
}

function visibleAccessLogEntries(): ParsedAccessLogEntry[] {
  if (!lastAccessLogs?.available) return [];

  if (
    visibleAccessLogCache
    && visibleAccessLogCache.logs === lastAccessLogs
    && visibleAccessLogCache.errorOnly === logsErrorOnly
    && visibleAccessLogCache.slowOnly === logsSlowOnly
    && visibleAccessLogCache.timeWindow === logsTimeWindow
  ) {
    return visibleAccessLogCache.entries;
  }

  const entries = accessLogEntriesForTimeWindow(parsedAccessLogEntries())
    .filter((entry) => !logsErrorOnly || isImportantAccessLogEntry(entry))
    .filter((entry) => !logsSlowOnly || isSlowAccessLogEntry(entry))
    .toReversed();
  visibleAccessLogCache = { logs: lastAccessLogs, errorOnly: logsErrorOnly, slowOnly: logsSlowOnly, timeWindow: logsTimeWindow, entries };

  return entries;
}

function parsedAccessLogEntries(): ParsedAccessLogEntry[] {
  if (!lastAccessLogs?.available) return [];

  if (parsedAccessLogCache && parsedAccessLogCache.logs === lastAccessLogs) {
    return parsedAccessLogCache.entries;
  }

  const entries = parseAccessLogOutput(lastAccessLogs.output || "").filter((entry) => entry.parsed);
  parsedAccessLogCache = { logs: lastAccessLogs, entries };

  return entries;
}

function formatMatchedRouteLines(source: CaddySource | undefined, entry: ParsedAccessLogEntry | undefined): string[] {
  if (!source || !entry) return [];

  const route = findMatchedRoute(source, entry);
  if (!route) return ["No route match inferred."];

  const actions = route.actions.map((action) => action.label).join("; ");
  return [`${route.matcher} → ${actions}`];
}

function findMatchedRoute(
  source: CaddySource,
  entry: ParsedAccessLogEntry,
): CaddySource["routes"][number] | undefined {
  const fallback = source.routes.find((route) => route.matcher === "everything else" || route.matcher === "all requests");

  for (const route of source.routes) {
    if (route === fallback) continue;
    if (routeMatchesAccessLog(route, entry)) return route;
  }

  return fallback;
}

function routeMatchesAccessLog(route: CaddySource["routes"][number], entry: ParsedAccessLogEntry): boolean {
  const matcher = route.matcher;
  const path = accessLogPath(entry.uri);
  if (!path) return false;

  if (matcher.includes("method ") && entry.method) {
    const methods = matcher.match(/method\s+([^;+]+)/)?.[1]?.split(/[,\s]+/).filter(Boolean) || [];
    if (methods.length > 0 && !methods.includes(entry.method)) return false;
  }

  const pathMatchers = [...matcher.matchAll(/path\s+([^;+]+)/g)]
    .flatMap((match) => match[1]?.split(",") || [])
    .map((value) => value.trim())
    .filter(Boolean);

  if (pathMatchers.length === 0) return false;

  return pathMatchers.some((pathMatcher) => pathMatches(path, pathMatcher));
}

function accessLogPath(uri: string | undefined): string | undefined {
  if (!uri) return undefined;

  try {
    return new URL(uri, "http://lazycaddy.local").pathname;
  } catch {
    return uri.split("?")[0] || uri;
  }
}

function pathMatches(path: string, matcher: string): boolean {
  if (matcher === "*" || matcher === "/*") return true;
  if (matcher.endsWith("*")) return path.startsWith(matcher.slice(0, -1));

  return path === matcher;
}

function serviceLogsContent(): StyledText {
  const lines: TextChunk[][] = [[sectionTitleChunk("Service logs")]];

  if (logsRefreshing && !lastLogs) {
    lines.push([warningChunk("● "), normalChunk("Fetching caddy.service logs...")]);
    return styledLines(lines);
  }

  if (!lastLogs) {
    lines.push([mutedChunk("○ "), normalChunk("No logs loaded yet.")]);
    return styledLines(lines);
  }

  const sourceText = lastLogs.ok
    ? `${timeLabel(lastLogs.fetchedAt)} in ${lastLogs.durationMs}ms`
    : lastLogs.error || "journalctl failed";
  lines.push([lastLogs.ok ? successChunk("● ") : errorChunk("● "), normalChunk(sourceText)]);

  const entries = parseServiceLogOutput(lastLogs.output || lastLogs.error || "")
    .filter((entry) => !logsErrorOnly || isImportantServiceLogEntry(entry))
    .toReversed();

  if (entries.length === 0) {
    lines.push([mutedChunk(logsErrorOnly ? "No error/warning service log entries found." : "No service log entries.")]);
    return styledLines(lines);
  }

  lines.push([mutedChunk(serviceLogTableHeader())]);
  lines.push(...entries.map(serviceLogRowChunks));
  lines.push([mutedChunk(`${entries.length} entries${logsErrorOnly ? " · errors only" : ""}`)]);

  return styledLines(lines);
}

function serviceLogTableHeader(): string {
  return [
    "TYPE".padEnd(4),
    "TIME".padEnd(8),
    "LOGGER".padEnd(serviceLogLoggerWidth()),
    "MESSAGE",
  ].join(" ");
}

function serviceLogRowChunks(entry: ParsedServiceLogEntry): TextChunk[] {
  const kind = serviceLogKind(entry);
  const time = (entry.timestamp ? formatRequestTime(entry.timestamp) : "-").padEnd(8).slice(0, 8);
  const logger = truncateMiddleOneLine(entry.logger || entry.unit || "caddy", serviceLogLoggerWidth()).padEnd(serviceLogLoggerWidth());
  const suffix = entry.error ? `  ${entry.error}` : entry.file ? `  ${entry.file}${entry.line ? `:${entry.line}` : ""}` : "";
  const rawMessage = entry.message || entry.error || entry.raw || "-";
  const message = truncateOneLine(`${rawMessage}${suffix}`, serviceLogMessageWidth());

  return [
    serviceLogKindChunk(kind, kind.padEnd(4)),
    mutedChunk(" "),
    mutedChunk(time),
    mutedChunk(" "),
    normalChunk(logger),
    mutedChunk(" "),
    normalChunk(message),
  ];
}

function serviceLogKindChunk(kind: ReturnType<typeof serviceLogKind>, value: string): TextChunk {
  if (kind === "ERR") return errorChunk(value);
  if (kind === "WARN") return warningChunk(value);
  if (kind === "OK") return successChunk(value);

  return mutedChunk(value);
}

function serviceLogLoggerWidth(): number {
  return Math.min(24, Math.max(12, Math.floor(contentWidth() * 0.22)));
}

function serviceLogMessageWidth(): number {
  return Math.max(20, contentWidth() - serviceLogLoggerWidth() - 15);
}

function configContent(source: CaddySource | undefined, block: CaddyfileSourceBlock | undefined): StyledText {
  return composeStyledBlocks(
    configSummaryContent(source, block),
    styledLines([
      [sectionTitleChunk("Caddyfile block")],
      ...formatCaddyfileBlock(caddyfileCorrelation, block).map(caddyfileLineChunks),
    ]),
    activeConfigSummaryContent(source),
  );
}

function systemContent(): StyledText {
  return composeStyledBlocks(
    serviceStatusContent(),
    serviceConfigContent(),
    serviceValidationContent(),
    serviceAdminContent(),
    serviceDiscoveryContent(),
    serviceLogsContent(),
  );
}

function composeStyledBlocks(...blocks: StyledText[]): StyledText {
  const chunks: TextChunk[] = [];

  blocks.forEach((block, index) => {
    chunks.push(...block.chunks);
    if (index < blocks.length - 1) chunks.push(normalChunk("\n\n"));
  });

  return new StyledText(chunks);
}

function configSummaryContent(
  source: CaddySource | undefined,
  block: CaddyfileSourceBlock | undefined,
): StyledText {
  if (!source) {
    return styledLines([
      [sectionTitleChunk("Config")],
      ...compactDetailRows([
        detailFieldLine("Config", discovery.configPath || discovery.command?.configPath || "not discovered"),
        detailFieldLine("Adapter", discovery.adapter || discovery.command?.adapter || "caddyfile/default"),
      ]),
    ]);
  }

  return styledLines([
    [sectionTitleChunk("Config source")],
    ...compactDetailRows([
      detailFieldLine("Host", sourceLabel(source), bold(normalChunk(sourceLabel(source)))),
      detailFieldLine("Caddyfile", caddyfileLocation(caddyfileCorrelation, block) || caddyfileCorrelation.error || "not correlated"),
      detailFieldLine("Block", block ? `lines ${block.line}-${block.endLine || "?"}` : "not correlated"),
      detailFieldLine("Adapter", discovery.adapter || discovery.command?.adapter || "caddyfile/default"),
    ]),
  ]);
}

function activeConfigSummaryContent(source: CaddySource | undefined): StyledText {
  if (!source) return styledLines([[sectionTitleChunk("Active config")], [mutedChunk("No source selected.")]]);

  const lines: TextChunk[][] = [
    [sectionTitleChunk("Active runtime config")],
    ...compactDetailRows([
      detailFieldLine("Server", source.serverName),
      detailFieldLine("Listener", source.listen.join(", ") || "default"),
      detailFieldLine("Routes", source.routes.length),
      detailFieldLine("Proxies", source.proxyCount),
    ]),
  ];

  if (source.routes.length > 0) {
    lines.push([]);
    lines.push([sectionTitleChunk("Routes")]);
    for (const [index, route] of source.routes.entries()) {
      lines.push([mutedChunk(`${index + 1}. `.padStart(3)), normalChunk(route.matcher)]);
      lines.push([mutedChunk("   ↳ "), normalChunk(route.actions.map((action) => action.label).join("; ") || "no direct handlers")]);
    }
  }

  return styledLines(lines);
}

function caddyfileLineChunks(line: string): TextChunk[] {
  const match = /^(\s*\d+\s*│\s?)(.*)$/.exec(line);
  if (!match) return [mutedChunk(line)];

  return [mutedChunk(match[1] || ""), ...caddyfileSyntaxChunks(match[2] || "")];
}

function caddyfileSyntaxChunks(code: string): TextChunk[] {
  const commentIndex = code.indexOf("#");
  const beforeComment = commentIndex >= 0 ? code.slice(0, commentIndex) : code;
  const comment = commentIndex >= 0 ? code.slice(commentIndex) : "";
  const match = /^(\s*)(\S+)(.*)$/.exec(beforeComment);
  const chunks: TextChunk[] = [];

  if (!match) {
    if (beforeComment) chunks.push(normalChunk(beforeComment));
  } else if (match[2] === "}") {
    chunks.push(normalChunk(match[1] || ""), mutedChunk(match[2]), normalChunk(match[3] || ""));
  } else {
    chunks.push(normalChunk(match[1] || ""), bold(fg("#2563eb")(match[2] || "")), normalChunk(match[3] || ""));
  }

  if (comment) chunks.push(mutedChunk(comment));
  return chunks.length > 0 ? chunks : [normalChunk(code)];
}

function accessLogHelper(source: CaddySource | undefined): string {
  const host = source ? sourceLabel(source) : "example.com";
  const fileName = sanitizeLogFileName(host);
  const block = findCaddyfileSource(caddyfileCorrelation, source);
  const location = caddyfileLocation(caddyfileCorrelation, block);

  return [
    "Access log helper:",
    `Add this inside the ${host} site block${location ? ` (${location})` : ""}, then validate/reload:`,
    "",
    "log {",
    `  output file /var/log/caddy/${fileName}.access.log`,
    "  format console",
    "}",
  ].join("\n");
}

function sanitizeLogFileName(value: string): string {
  return value.replace(/[^a-z0-9_.-]+/gi, "_").replace(/^_+|_+$/g, "") || "site";
}

function serviceStatusContent(): StyledText {
  const service = discovery.service;
  if (!service) {
    return styledLines([
      [sectionTitleChunk("Caddy service")],
      [warningChunk("○ "), normalChunk("caddy.service not inspected")],
      ...compactDetailRows([
        detailFieldLine("Reason", discovery.sourceLabel),
      ]),
    ]);
  }

  const state = [service.activeState, service.subState].filter(Boolean).join("/") || "unknown";

  return styledLines([
    [sectionTitleChunk("Caddy service")],
    [serviceStateDotChunk(service.activeState), normalChunk(" "), serviceStateChunk(state)],
    ...compactDetailRows([
      detailFieldLine("Loaded", service.loadState || "unknown"),
      detailFieldLine("PID", service.mainPid && service.mainPid > 0 ? service.mainPid : "n/a"),
      detailFieldLine("Unit", service.fragmentPath || "n/a"),
    ]),
  ]);
}

function serviceConfigContent(): StyledText {
  const command = discovery.command;
  const executable = command?.argv[0] || discovery.service?.argv?.[0];

  return styledLines([
    [sectionTitleChunk("Startup config")],
    ...compactDetailRows([
      detailFieldLine("Config", discovery.configPath || command?.configPath || "not discovered"),
      detailFieldLine("Adapter", discovery.adapter || command?.adapter || "caddyfile/default"),
      detailFieldLine("Resume", command?.resume ? "yes" : "no", command?.resume ? warningChunk("yes") : mutedChunk("no")),
      detailFieldLine("Command", executable || "not available"),
      detailFieldLine("Args", discovery.service?.argvSource || (command ? "process" : "not available")),
    ]),
  ]);
}

function serviceValidationContent(): StyledText {
  if (validating) {
    return styledLines([[sectionTitleChunk("Validation")], [warningChunk("● "), normalChunk("Validating Caddy config...")]]);
  }

  if (!lastValidation) {
    return styledLines([
      [sectionTitleChunk("Validation")],
      [mutedChunk("○ "), normalChunk("Not run yet. Press v to validate.")],
      ...compactDetailRows([
        detailFieldLine("Config", discovery.configPath || discovery.command?.configPath || "not discovered"),
      ]),
    ]);
  }

  const validation = lastValidation;

  return styledLines([
    [sectionTitleChunk("Validation")],
    [validation.ok ? successChunk("● ") : errorChunk("● "), validation.ok ? successChunk("OK") : errorChunk(validation.skipped ? "SKIPPED" : "FAILED")],
    ...compactDetailRows([
      detailFieldLine("Command", validation.command.length > 0 ? validation.command.join(" ") : "not run"),
      detailFieldLine("Ran", `${timeLabel(validation.ranAt)} in ${validation.durationMs}ms`),
    ]),
    ...validationOutputLines(validation.output).map((line) => [mutedChunk("  "), validation.ok ? successChunk(line) : errorChunk(line)]),
  ]);
}

function serviceAdminContent(): StyledText {
  const proxyCount = sources.reduce((total, source) => total + source.proxyCount, 0);
  const status = serviceAdminStatusLabel();

  return styledLines([
    [sectionTitleChunk("Admin API")],
    [adminStatusChunk(), normalChunk(" "), normalChunk(status)],
    ...compactDetailRows([
      detailFieldLine("Endpoint", discovery.adminUrl || "none"),
      detailFieldLine("Active", lastLoad.ok ? `${sources.length} sources · ${proxyCount} proxies` : "n/a"),
      detailFieldLine("Fetched", lastLoad.ok ? `${timeLabel(lastLoad.fetchedAt)} in ${lastLoad.durationMs}ms` : "n/a"),
      detailFieldLine("Source", discovery.sourceLabel),
    ]),
  ]);
}

function serviceDiscoveryContent(): StyledText {
  const notes = discovery.notes.length > 0 ? discovery.notes : ["No discovery notes."];

  return styledLines([
    [sectionTitleChunk("Discovery")],
    ...notes.map((note) => [mutedChunk("• "), normalChunk(note)]),
  ]);
}

function serviceStateDotChunk(activeState: string | undefined): TextChunk {
  if (activeState === "active") return successChunk("●");
  if (activeState === "activating" || activeState === "reloading") return warningChunk("●");
  if (activeState === "failed" || activeState === "inactive") return errorChunk("●");

  return mutedChunk("●");
}

function serviceStateChunk(state: string): TextChunk {
  if (state.startsWith("active/")) return successChunk(state);
  if (state.includes("failed") || state.startsWith("inactive")) return errorChunk(state);
  if (state.includes("activating") || state.includes("reloading")) return warningChunk(state);

  return mutedChunk(state);
}

function adminStatusChunk(): TextChunk {
  if (refreshing) return warningChunk("●");
  if (lastLoad.ok) return successChunk("●");
  if (discovery.disabled) return mutedChunk("●");

  return errorChunk("●");
}

function validationOutputLines(output: string): string[] {
  const lines = output.split(/\r?\n/).filter(Boolean).slice(0, 3);

  return lines.length > 0 ? lines : ["Config is valid."];
}

function serviceAdminStatusLabel(): string {
  if (refreshing) return "refreshing";
  if (lastLoad.ok) return `OK ${lastLoad.statusCode ?? ""}`.trim();
  if (discovery.disabled) return "disabled";

  return lastLoad.error || "unavailable";
}

function serviceCardTitle(source: CaddySource): string {
  return sourceLabel(source);
}

function serviceReachabilityStatus(source: CaddySource): "REACHABLE" | "UNREACHABLE" | "CHECKING" | "UNKNOWN" {
  if (healthCheckingSourceId === source.id) return "CHECKING";

  const results = upstreamHealthBySource.get(source.id);
  if (!results) return "UNKNOWN";
  if (results.some((result) => result.status === "down")) return "UNREACHABLE";
  if (results.some((result) => result.status === "ok")) return "REACHABLE";

  return "UNKNOWN";
}

function serviceTargetSummary(source: CaddySource): string {
  const targets = sourceUpstreamLabels(source);

  return targets.length > 0 ? targets.join(", ") : "none";
}

function wrapText(value: string, width: number): string[] {
  const maxWidth = Math.max(1, width);
  const text = value.replace(/\r?\n/g, " ").trim();
  if (!text) return [""];

  const lines: string[] = [];
  let remaining = text;

  while (remaining.length > maxWidth) {
    let splitIndex = remaining.lastIndexOf(" ", maxWidth);
    if (splitIndex <= 0) splitIndex = maxWidth;

    lines.push(remaining.slice(0, splitIndex).trimEnd());
    remaining = remaining.slice(splitIndex).trimStart();
  }

  lines.push(remaining);
  return lines;
}

function truncateOneLine(value: string, maxLength: number): string {
  if (value.length <= maxLength) return value;
  if (maxLength <= 1) return "…";

  return `${value.slice(0, maxLength - 1)}…`;
}

function truncateMiddleOneLine(value: string, maxLength: number): string {
  if (value.length <= maxLength) return value;
  if (maxLength <= 1) return "…";
  if (maxLength <= 4) return truncateOneLine(value, maxLength);

  const keep = maxLength - 1;
  const startLength = Math.ceil(keep / 2);
  const endLength = Math.floor(keep / 2);

  return `${value.slice(0, startLength)}…${value.slice(-endLength)}`;
}

function sourceUpstreamLabels(source: CaddySource): string[] {
  return [
    ...new Set(
      source.routes.flatMap((route) =>
        route.actions.flatMap((action) => action.upstreams?.map((upstream) => upstream.label) || []),
      ),
    ),
  ];
}

function formatConfigSectionLines(source?: CaddySource): string[] {
  if (!source) return ["No source selected"];

  const block = findCaddyfileSource(caddyfileCorrelation, source);

  const location = caddyfileLocation(caddyfileCorrelation, block);

  return [
    `Server:    ${source.serverName}`,
    `Listener:  ${source.listen.join(", ") || "default"}`,
    `Caddyfile: ${location || caddyfileCorrelation.error || "not correlated"}`,
    `Block:     ${block ? `lines ${block.line}-${block.endLine || "?"}` : "not correlated"}`,
  ];
}

function adminUnavailableMessage(): string {
  if (discovery.disabled) {
    return "The discovered Caddy service config disables the Admin API, so lazycaddy cannot read the active config.";
  }

  return "No queryable Caddy Admin API endpoint was discovered. Use --admin-url or CADDY_ADMIN_API to override.";
}

function timeLabel(date: Date): string {
  return date.toLocaleTimeString(undefined, { hour12: false });
}

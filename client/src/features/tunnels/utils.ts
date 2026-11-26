import { operationBlockerMessage } from "@/shared/api/client";
import type { FrpTunnelDetail, TunnelStatusTone } from "./types";

export const TUNNEL_STATUS_MAP: Record<string, { label: string; tone: TunnelStatusTone }> = {
  running: { label: "运行中", tone: "success" },
  stopped: { label: "已停止", tone: "neutral" },
  starting: { label: "启动中", tone: "info" },
  stopping: { label: "停止中", tone: "warning" },
  failed: { label: "异常失败", tone: "danger" },
  cleanup_required: { label: "需清理", tone: "warning" },
  pending_confirmation: { label: "待确认", tone: "info" },
  cancelled: { label: "已取消", tone: "neutral" },
};

const TUNNEL_STATUS_KEYS: Record<
  string,
  { key: string; defaultLabel: string; tone: TunnelStatusTone }
> = {
  running: { key: "tunnels.statusRunning", defaultLabel: "运行中", tone: "success" },
  stopped: { key: "tunnels.statusStopped", defaultLabel: "已停止", tone: "neutral" },
  starting: { key: "tunnels.statusStarting", defaultLabel: "启动中", tone: "info" },
  stopping: { key: "tunnels.statusStopping", defaultLabel: "停止中", tone: "warning" },
  failed: { key: "tunnels.statusFailed", defaultLabel: "异常失败", tone: "danger" },
  cleanup_required: {
    key: "tunnels.statusCleanupRequired",
    defaultLabel: "需清理",
    tone: "warning",
  },
  pending_confirmation: {
    key: "tunnels.statusPendingConfirm",
    defaultLabel: "待确认",
    tone: "info",
  },
  cancelled: { key: "tunnels.statusCancelled", defaultLabel: "已取消", tone: "neutral" },
};

const ACTIVE_STATUSES = new Set([
  "pending_confirmation",
  "starting",
  "running",
  "stopping",
  "cleanup_required",
]);

export function getTunnelStatusMeta(
  status?: string,
  t?: (key: any) => string,
): {
  label: string;
  tone: TunnelStatusTone;
} {
  const meta = TUNNEL_STATUS_KEYS[status ?? ""];
  if (meta) {
    return {
      label: t ? t(meta.key) : meta.defaultLabel,
      tone: meta.tone,
    };
  }
  return {
    label: status ?? (t ? t("tunnels.statusStopped") : "已停止"),
    tone: "neutral" as TunnelStatusTone,
  };
}

/** 服务端只允许在同一服务器上存在一个活动运行；活动期间配置不可编辑或删除。 */
export function isTunnelActive(status?: string): boolean {
  return status ? ACTIVE_STATUSES.has(status) : false;
}

export function canStartTunnel(status?: string): boolean {
  return !isTunnelActive(status);
}

export function canStopTunnel(status?: string): boolean {
  return status === "starting" || status === "running" || status === "cleanup_required";
}

/** 预检阻断码复用共享翻译，保证与操作通知中的原因完全一致。 */
export function tunnelBlockReasonLabel(code: string): string {
  return operationBlockerMessage(code);
}

export function formatFriendlyTime(dateStr?: string | null): string {
  if (!dateStr) return "—";
  const date = new Date(dateStr);
  if (Number.isNaN(date.getTime())) return String(dateStr);

  const pad = (n: number) => String(n).padStart(2, "0");
  const year = date.getFullYear();
  const month = pad(date.getMonth() + 1);
  const day = pad(date.getDate());
  const hours = pad(date.getHours());
  const minutes = pad(date.getMinutes());
  const seconds = pad(date.getSeconds());

  return `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`;
}

export function formatRelativeTime(
  dateStr?: string | null,
  t?: (key: string, params?: any) => string,
): string {
  if (!dateStr) return "—";
  const date = new Date(dateStr);
  if (Number.isNaN(date.getTime())) return "—";

  const diffMs = Date.now() - date.getTime();
  if (diffMs < 0) return t ? t("tunnels.timeJustNow") : "刚刚";
  const diffSec = Math.floor(diffMs / 1000);
  if (diffSec < 60) {
    return t
      ? t("tunnels.timeSecAgo", { count: Math.max(1, diffSec) })
      : `${Math.max(1, diffSec)} 秒前`;
  }
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) {
    return t ? t("tunnels.timeMinAgo", { count: diffMin }) : `${diffMin} 分钟前`;
  }
  const diffHours = Math.floor(diffMin / 60);
  if (diffHours < 24) {
    return t ? t("tunnels.timeHoursAgo", { count: diffHours }) : `${diffHours} 小时前`;
  }
  const diffDays = Math.floor(diffHours / 24);
  if (diffDays < 30) {
    return t ? t("tunnels.timeDaysAgo", { count: diffDays }) : `${diffDays} 天前`;
  }
  return formatFriendlyTime(dateStr).slice(0, 10);
}

export function formatDuration(
  startStr?: string | null,
  endStr?: string | null,
  t?: (key: string, params?: any) => string,
): string {
  if (!startStr) return "—";
  const start = new Date(startStr).getTime();
  if (Number.isNaN(start)) return "—";
  const end = endStr ? new Date(endStr).getTime() : Date.now();
  if (Number.isNaN(end) || end < start) return "—";

  const totalSec = Math.floor((end - start) / 1000);
  if (totalSec < 60) {
    return t
      ? t("tunnels.durationSec", { count: Math.max(1, totalSec) })
      : `${Math.max(1, totalSec)} 秒`;
  }
  const min = Math.floor(totalSec / 60);
  const sec = totalSec % 60;
  if (min < 60) {
    return sec > 0
      ? t
        ? t("tunnels.durationMinSec", { min, sec })
        : `${min} 分 ${sec} 秒`
      : t
        ? t("tunnels.durationMin", { min })
        : `${min} 分钟`;
  }
  const hours = Math.floor(min / 60);
  const remainingMin = min % 60;
  if (hours < 24) {
    return remainingMin > 0
      ? t
        ? t("tunnels.durationHoursMin", { hours, min: remainingMin })
        : `${hours} 小时 ${remainingMin} 分`
      : t
        ? t("tunnels.durationHours", { hours })
        : `${hours} 小时`;
  }
  const days = Math.floor(hours / 24);
  const remainingHours = hours % 24;
  return t
    ? t("tunnels.durationDaysHours", { days, hours: remainingHours })
    : `${days} 天 ${remainingHours} 小时`;
}

export function getMethodTone(method?: string): TunnelStatusTone {
  const m = (method ?? "").toUpperCase();
  switch (m) {
    case "GET":
      return "info";
    case "POST":
      return "success";
    case "PUT":
    case "PATCH":
      return "warning";
    case "DELETE":
      return "danger";
    default:
      return "neutral";
  }
}

export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

export type FrpcConfigOptions = {
  /** 无公网域名时用于直连的服务器公网地址。 */
  serverHost: string;
  /** Traefik 对外 HTTP/HTTPS 端口，用于换算 `serverPort`；默认 80/443。 */
  httpPort?: number;
  httpsPort?: number;
  /** 可选 DNS，与 Worker 生成的 `dnsServer` 对齐。 */
  dnsServer?: string;
};

/** 与 Go 侧 `frpcConfig` 同构的参考配置；不包含任何真实 token。 */
export function generateFrpcConfig(tunnel: FrpTunnelDetail, options: FrpcConfigOptions): string {
  // 首条绑定域名的映射决定 Traefik 控制入口，代理条目按名称排序。
  const legacyIngress = tunnel.latestRun?.legacyIngress === true;
  const control = legacyIngress
    ? tunnel.mappings[0]
    : tunnel.mappings.find((mapping) => mapping.publicDomain?.trim());
  const mappings = [...tunnel.mappings].sort((left, right) =>
    left.name < right.name ? -1 : left.name > right.name ? 1 : 0,
  );
  const secure = control?.publicScheme === "https";
  const serverHost = control?.publicDomain?.trim() || options.serverHost;
  const gatewayPort = secure ? (options.httpsPort ?? 443) : (options.httpPort ?? 80);
  const serverPort = control ? gatewayPort : tunnel.frpsBindPort;
  const protocol = control ? (secure ? "wss" : "websocket") : "tcp";
  const lines = [`serverAddr = ${JSON.stringify(serverHost)}`, `serverPort = ${serverPort}`];
  if (options.dnsServer?.trim())
    lines.push(`dnsServer = ${JSON.stringify(options.dnsServer.trim())}`);
  lines.push(
    'log.to = "console"',
    'auth.method = "token"',
    'auth.token = "{{ .Envs.FRP_TOKEN }}"',
    `transport.protocol = "${protocol}"`,
    "transport.tls.enable = true",
    "",
  );
  for (const mapping of mappings) {
    const localHost = mapping.localHost || "127.0.0.1";
    lines.push(
      "[[proxies]]",
      `name = ${JSON.stringify(mapping.name)}`,
      'type = "tcp"',
      `remotePort = ${mapping.frpsProxyPort}`,
    );
    if (mapping.targetType === "http_probe" && !legacyIngress) {
      const localAddr = `${localHost.includes(":") ? `[${localHost}]` : localHost}:${mapping.localPort}`;
      const runId = tunnel.latestRun?.id ?? "{{ .Envs.FRP_TUNNEL_RUN_ID }}";
      lines.push(
        "[proxies.plugin]",
        'type = "http2http"',
        `localAddr = ${JSON.stringify(localAddr)}`,
        "[proxies.plugin.requestHeaders.set]",
        `X-IQuOps-Tunnel-ID = ${JSON.stringify(tunnel.id)}`,
        `X-IQuOps-Tunnel-Run-ID = ${JSON.stringify(runId)}`,
        `X-IQuOps-Tunnel-Mapping = ${JSON.stringify(mapping.name)}`,
      );
    } else {
      lines.push(`localIP = ${JSON.stringify(localHost)}`, `localPort = ${mapping.localPort}`);
    }
    lines.push("");
  }
  return lines.join("\n");
}

export const FRPC_RUN_COMMAND = "frpc -c ./frpc.toml";

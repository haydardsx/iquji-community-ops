import type { MonitoringAgent, MonitoringProject, MonitoringServer, SwarmOverview } from "./api";

export type OverallHealthLevel = "healthy" | "warning" | "critical";

export type SystemHealthSummary = {
  level: OverallHealthLevel;
  title: string;
  description: string;
  totalServers: number;
  onlineServers: number;
  offlineServers: number;
  unknownServers: number;
  serverOnlinePercent: number;
  totalProjects: number;
  runningProjects: number;
  idleProjects: number;
  errorProjects: number;
  swarmNodeCount: number;
  swarmClusterCount: number | null;
  swarmManagerCount: number;
  swarmWorkerCount: number;
  swarmStatus: NonNullable<SwarmOverview["status"]>;
  totalAgents: number;
  onlineAgents: number;
  staleAgents: number;
  offlineAgents: number;
  unknownAgents: number;
  totalAlerts: number;
};

/**
 * 汇总计算系统全景健康态势
 */
export function calculateSystemHealth(
  projects: MonitoringProject[] = [],
  servers: MonitoringServer[] = [],
  swarm: SwarmOverview = {},
  agents: MonitoringAgent[] = [],
  t?: (key: string, params?: any) => string,
): SystemHealthSummary {
  const totalServers = servers.length;
  const onlineServers = servers.filter((s) => s.connectionStatus === "online").length;
  const offlineServers = servers.filter((s) => s.connectionStatus === "offline").length;
  const unknownServers = totalServers - onlineServers - offlineServers;
  const serverOnlinePercent =
    totalServers > 0 ? Math.round((onlineServers / totalServers) * 100) : 100;

  let runningProjects = 0;
  let idleProjects = 0;
  let errorProjects = 0;
  let totalAlerts = 0;

  for (const p of projects) {
    if (p.alerts && p.alerts.length > 0) {
      totalAlerts += p.alerts.length;
    }
    if (p.status === "running" || p.status === "success") {
      runningProjects++;
    } else if (p.status === "failed" || p.status === "danger") {
      errorProjects++;
    } else {
      idleProjects++;
    }
  }

  for (const s of servers) {
    if (s.alerts && s.alerts.length > 0) {
      totalAlerts += s.alerts.length;
    }
  }

  const installedAgents = agents.filter(
    (agent) => !["not_installed", "uninstalled"].includes(agent.status),
  );
  const totalAgents = installedAgents.length;
  const onlineAgents = installedAgents.filter((agent) => agent.status === "online").length;
  const staleAgents = installedAgents.filter((agent) => agent.status === "stale").length;
  const offlineAgents = installedAgents.filter((agent) => agent.status === "offline").length;
  const unknownAgents = totalAgents - onlineAgents - staleAgents - offlineAgents;
  const swarmNodes = swarm.nodes ?? [];
  const clusterCount =
    swarm.clusterCount ?? new Set(swarmNodes.map((node) => node.clusterId).filter(Boolean)).size;

  let level: OverallHealthLevel = "healthy";
  let title = t ? t("monitoring.allNormal") : "全链路系统运行正常";
  let description = t
    ? t("monitoring.allNormalDesc")
    : "所有已纳管服务器探针在线，微服务与运行时实例均处于健康受控状态。";

  if (errorProjects > 0 || totalAlerts > 0) {
    level = "critical";
    if (t) {
      const servicesPart =
        errorProjects > 0 ? t("monitoring.servicesAbnormalCount", { count: errorProjects }) : "";
      const alertsPart =
        totalAlerts > 0 ? t("monitoring.activeAlertsCount", { count: totalAlerts }) : "";
      const separator = servicesPart && alertsPart ? "，" : "";
      title = t("monitoring.criticalTitle", {
        services: servicesPart,
        separator,
        alerts: alertsPart,
      });
      description = t("monitoring.detectedAbnormalDesc");
    } else {
      title = `检测到 ${errorProjects > 0 ? `${errorProjects} 个服务异常` : ""}${totalAlerts > 0 ? `，${totalAlerts} 个活动告警` : ""}`;
      description = "请检查相关微服务容器与告警指标，及时排除故障以防影响线上环境。";
    }
  } else if (offlineServers > 0 || offlineAgents > 0 || staleAgents > 0) {
    level = "warning";
    if (t) {
      title = t("monitoring.warningTitle", {
        servers: offlineServers,
        agents: offlineAgents + staleAgents,
      });
      description = t("monitoring.detectedWarningDesc");
    } else {
      title = `检测到 ${offlineServers} 台主机离线，${offlineAgents + staleAgents} 个探针采集异常`;
      description =
        "部分受管服务器无法持续上报运行时资源数据，建议检查主机网络或重新安装监控 Agent。";
    }
  } else if (
    unknownServers > 0 ||
    unknownAgents > 0 ||
    swarm.status === "error" ||
    swarm.status === "stale" ||
    swarm.status === "unverified"
  ) {
    level = "warning";
    title = t ? t("monitoring.incompleteHealthTitle") : "部分监控状态待核验";
    description = t ? t("monitoring.incompleteHealthDesc") : "请查看主机、探针和集群的最近采样。";
  }

  return {
    level,
    title,
    description,
    totalServers,
    onlineServers,
    offlineServers,
    unknownServers,
    serverOnlinePercent,
    totalProjects: projects.length,
    runningProjects,
    idleProjects,
    errorProjects,
    swarmNodeCount: swarmNodes.length,
    swarmClusterCount:
      clusterCount || (swarm.status === "not_enabled" || swarm.status === "ready" ? 0 : null),
    swarmManagerCount: swarmNodes.filter((node) => node.role === "manager").length,
    swarmWorkerCount: swarmNodes.filter((node) => node.role === "worker").length,
    swarmStatus: swarm.status ?? "unverified",
    totalAgents,
    onlineAgents,
    staleAgents,
    offlineAgents,
    unknownAgents,
    totalAlerts,
  };
}

/**
 * 格式化百分比数值
 */
export function formatPercent(value?: number | null): string {
  if (value === undefined || value === null || !Number.isFinite(value)) {
    return "--";
  }
  return `${Math.round(value)}%`;
}

/**
 * 根据资源使用率确定色彩级别
 */
export function getResourceTone(
  value?: number | null,
): "success" | "warning" | "danger" | "neutral" {
  if (value === undefined || value === null || !Number.isFinite(value)) {
    return "neutral";
  }
  if (value >= 85) return "danger";
  if (value >= 70) return "warning";
  return "success";
}

/**
 * 人性化格式化时间
 */
export function formatTimeAgo(
  isoString?: string | null,
  t?: (key: string, params?: any) => string,
): string {
  if (!isoString) return t ? t("monitoring.notSampled") : "未采样";
  const date = new Date(isoString);
  const time = date.getTime();
  if (Number.isNaN(time)) return isoString;

  const diffMs = Date.now() - time;
  if (diffMs < 0) return t ? t("monitoring.timeJustNow") : "刚刚";
  const diffSec = Math.floor(diffMs / 1000);
  if (diffSec < 45) return t ? t("monitoring.timeJustNow") : "刚刚";
  if (diffSec < 90) return t ? t("monitoring.time1MinAgo") : "1 分钟前";
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return t ? t("monitoring.timeMinAgo", { m: diffMin }) : `${diffMin} 分钟前`;
  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) return t ? t("monitoring.timeHourAgo", { h: diffHour }) : `${diffHour} 小时前`;
  const diffDay = Math.floor(diffHour / 24);
  return t ? t("monitoring.timeDayAgo", { d: diffDay }) : `${diffDay} 天前`;
}

/**
 * 格式化字节大小
 */
export function formatBytes(bytes?: number | null): string {
  if (bytes === undefined || bytes === null || !Number.isFinite(bytes) || bytes <= 0) {
    return "--";
  }
  const units = ["B", "KB", "MB", "GB", "TB"];
  let val = bytes;
  let unitIndex = 0;
  while (val >= 1024 && unitIndex < units.length - 1) {
    val /= 1024;
    unitIndex++;
  }
  return `${val.toFixed(unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

/** 按当前界面语言和浏览器时区展示完整采样时间。 */
export function formatSampleTime(
  value?: string | null,
  locale = "zh-CN",
  timeZone?: string,
): string {
  if (!value) return "—";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "—";
  return new Intl.DateTimeFormat(locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
    timeZone,
  }).format(date);
}

export function agentStatusLabel(status: string, t?: (key: string) => string): string {
  if (t) {
    const keys: Record<string, string> = {
      online: "monitoring.probeOnline",
      offline: "monitoring.probeOffline",
      stale: "monitoring.probeStale",
      unknown: "monitoring.probeWaitFirst",
      not_installed: "monitoring.probeNotInstalled",
      uninstalled: "monitoring.probeNotInstalled",
      installing: "monitoring.probeInstalling",
    };
    const key = keys[status];
    return key ? t(key) : t("monitoring.probeUnknown");
  }
  const labels: Record<string, string> = {
    online: "探针在线",
    offline: "采集失败",
    stale: "心跳已过期",
    unknown: "等待首次采集",
    not_installed: "未安装探针",
    uninstalled: "未安装探针",
    installing: "正在安装",
  };
  return labels[status] ?? "状态待核验";
}

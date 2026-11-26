import type { IqujiIconName } from "@iquji-community/web-icons";
import type { ServerRuntimeMetrics } from "@/contracts/types";
import type { RuntimeCheck, ServerRuntimeComponent } from "./api";

import { swarmIdentityVerified } from "./swarm-connection";

export interface RuntimeComponentItem {
  key: ServerRuntimeComponent | "agent" | "ssh";
  label: string;
  desc: string;
  icon: IqujiIconName;
}

export function getRuntimeComponents(
  metrics: ServerRuntimeMetrics,
  t?: (key: string, params?: Record<string, string | number>) => string,
): RuntimeComponentItem[] {
  return [
    {
      key: "ssh",
      label: t ? "SSH" : "SSH 纳管通道",
      desc: runtimeComponentPresentation(metrics, "ssh", t).label,
      icon: "terminal",
    },
    {
      key: "docker",
      label: t ? t("servers.dockerEngine") : "Docker 引擎",
      desc:
        metrics.docker === true && metrics.dockerVersion
          ? t
            ? t("servers.versionWithNumber", { version: metrics.dockerVersion })
            : `版本 ${metrics.dockerVersion}`
          : runtimeComponentPresentation(metrics, "docker", t).label,
      icon: "container",
    },
    {
      key: "swarm",
      label: t ? t("swarm.membership") : "Swarm 集群成员关系",
      desc: swarmMembershipDescription(metrics, t),
      icon: "boxes",
    },
    {
      key: "traefik",
      label: t ? t("servers.traefikService") : "Traefik 流量网关",
      desc:
        metrics.ssh !== false && metrics.traefik === true && metrics.traefikService
          ? `${metrics.traefikService} · ${
              t
                ? t("servers.portWithNumber", {
                    port:
                      [metrics.traefikHttpPort, metrics.traefikHttpsPort]
                        .filter(Boolean)
                        .join("/") || "80/443",
                  })
                : `端口 ${[metrics.traefikHttpPort, metrics.traefikHttpsPort].filter(Boolean).join("/") || "80/443"}`
            }`
          : runtimeComponentPresentation(metrics, "traefik", t).label,
      icon: "network",
    },
    {
      key: "frps",
      label: t ? t("servers.frpsService") : "FRPS 穿透服务",
      desc: metrics.frpsIdle
        ? metrics.frpsExternal
          ? (t?.("servers.frpsExternalIdle") ?? "未开启隧道，请在服务器上卸载 FRPS")
          : (t?.("servers.frpsIdle") ?? "未开启隧道，请及时卸载 FRPS")
        : (metrics.frpsImage ?? runtimeComponentPresentation(metrics, "frps", t).label),
      icon: "network",
    },
    {
      key: "jenkins",
      label: t ? t("servers.jenkinsRunner") : "Jenkins 运行器",
      desc:
        metrics.ssh !== false && metrics.jenkins === true && metrics.jenkinsVersion
          ? t
            ? t("servers.versionWithNumber", { version: String(metrics.jenkinsVersion) })
            : `版本 ${metrics.jenkinsVersion}`
          : runtimeComponentPresentation(metrics, "jenkins", t).label,
      icon: "wrench",
    },
    {
      key: "agent",
      label: t ? t("monitoring.agentsTitle") : "监控采集 Agent",
      desc: agentDescription(metrics, t),
      icon: "activity",
    },
  ];
}

export function swarmMembership(metrics: ServerRuntimeMetrics) {
  if (metrics.ssh === false) return "unknown";
  if (metrics.swarmVerificationStatus === "not_joined") return "inactive";
  if (metrics.docker === false) return "unknown";
  if (swarmJoinIncomplete(metrics)) return "incomplete";
  if (metrics.swarm === true) {
    return metrics.swarmRole === "manager" || metrics.swarmRole === "worker"
      ? metrics.swarmRole
      : "member";
  }
  if (metrics.swarmVerificationStatus === "stale" || metrics.swarmVerificationError)
    return "unknown";
  if (metrics.swarmLocalNodeState === "inactive" || metrics.swarmRole === "none") return "inactive";
  return "unknown";
}

export function swarmControlPresentation(
  metrics: ServerRuntimeMetrics,
  t: (key: string) => string,
) {
  switch (swarmMembership(metrics)) {
    case "manager":
      if (!swarmIdentityVerified(metrics)) break;
      if (metrics.swarmControlAvailable === true)
        return { label: t("servers.managerControlAvailable"), tone: "success" as const };
      if (metrics.swarmControlAvailable === false)
        return { label: t("servers.runtimeUnavailable"), tone: "warning" as const };
      break;
    case "worker":
      if (!swarmIdentityVerified(metrics)) break;
      return { label: t("servers.managedByManager"), tone: "neutral" as const };
    case "inactive":
      return { label: t("servers.notEnabled"), tone: "neutral" as const };
  }
  return { label: t("swarm.statusUnverified"), tone: "neutral" as const };
}

export function swarmJoinIncomplete(metrics: ServerRuntimeMetrics) {
  return ["pending", "error"].includes(metrics.swarmLocalNodeState ?? "");
}

export function swarmLeavePresentation(metrics: ServerRuntimeMetrics, t: (key: string) => string) {
  if (swarmJoinIncomplete(metrics)) {
    return {
      label: t("servers.exitIncompleteJoin"),
      confirmLabel: t("servers.confirmExitIncompleteJoin"),
      description: t("servers.exitIncompleteDesc"),
    };
  }
  if (metrics.swarmRole === "worker") {
    return {
      label: t("servers.leaveCluster"),
      confirmLabel: t("servers.confirmLeaveCluster"),
      description: t("servers.workerLeaveDesc"),
    };
  }
  return {
    label: t("servers.dissolveCluster"),
    confirmLabel: t("servers.confirmDissolveCluster"),
    description: t("servers.dissolveClusterDesc"),
  };
}

export function swarmMembershipDescription(
  metrics: ServerRuntimeMetrics,
  t?: (key: string, params?: Record<string, string | number>) => string,
) {
  const membership = swarmMembership(metrics);
  if (membership === "incomplete") {
    return t ? t("swarm.statusUnverified") : "加入尚未完成，请检查原因后退出并重试";
  }
  if (membership === "inactive" || membership === "unknown") {
    return membership === "inactive"
      ? t
        ? t("swarm.statusNotEnabled")
        : "未加入集群"
      : t
        ? t("swarm.statusUnverified")
        : "集群状态待探测";
  }
  if (!swarmIdentityVerified(metrics) || membership === "member") {
    return t ? t("swarm.statusUnverified") : "角色待核验";
  }
  const cluster = metrics.swarmClusterId || (t ? t("swarm.clusterUnknown") : "集群身份未知");
  return `${membership} · ${cluster} · ${t ? t("state.ready") : "已核验"}`;
}

export function agentDescription(
  metrics: ServerRuntimeMetrics,
  t?: (key: string, params?: Record<string, string | number>) => string,
) {
  if (metrics.agent === true && metrics.ssh !== false) {
    return typeof metrics.agentVersion === "string" && metrics.agentVersion
      ? `${t ? t("servers.agentRunning") : "已运行"} · v${metrics.agentVersion}`
      : t
        ? t("monitoring.allNormal")
        : "监控采集正常";
  }
  return runtimeComponentPresentation(metrics, "agent", t).label;
}

export function runtimeComponentPresentation(
  metrics: ServerRuntimeMetrics,
  component: RuntimeComponentItem["key"],
  t?: (key: string, params?: Record<string, string | number>) => string,
) {
  const status = String(metrics[`${component}Status`] ?? "");
  if (metrics.ssh === false) {
    return {
      label:
        component === "ssh"
          ? (t?.("servers.sshUnreachable") ?? "SSH 无法连接")
          : (t?.("servers.runtimeUnverified") ?? "状态待核验"),
      tone: "warning" as const,
    };
  }
  if (component === "swarm" && swarmMembership(metrics) === "inactive") {
    return { label: t ? t("swarm.statusNotEnabled") : "未加入", tone: "neutral" as const };
  }
  if (component === "frps") {
    if (metrics.frpsIdle) {
      return { label: t?.("servers.frpsIdleBadge") ?? "待卸载", tone: "danger" as const };
    }
    if (metrics.frpsInUse && metrics.frpsPresent) {
      return { label: t?.("servers.frpsInUse") ?? "隧道使用中", tone: "success" as const };
    }
  }
  if (
    status === "docker_unavailable" ||
    (component === "docker" && (status === "unavailable" || (!status && metrics.docker === false)))
  ) {
    return { label: t?.("servers.runtimeUnavailable") ?? "暂不可用", tone: "warning" as const };
  }
  if (["unavailable", "error", "failed", "stopped", "exited"].includes(status)) {
    return { label: t ? t("state.degraded") : "运行异常", tone: "warning" as const };
  }
  if (component === "swarm" && metrics.swarmLocalNodeState === "error") {
    return { label: t ? t("state.failed") : "加入异常", tone: "warning" as const };
  }
  if (component === "swarm" && metrics.swarmLocalNodeState === "pending") {
    return { label: t ? t("state.starting") : "加入中", tone: "neutral" as const };
  }
  if (component === "swarm" && metrics.swarmVerificationError) {
    return { label: t ? t("state.failed") : "核验失败", tone: "warning" as const };
  }
  if (
    component === "swarm" &&
    metrics.swarm === true &&
    metrics.swarmVerificationStatus !== "verified"
  ) {
    return { label: t ? t("monitoring.unknown") : "待核验", tone: "neutral" as const };
  }
  if (["missing", "not_installed", "uninstalled"].includes(status)) {
    return { label: t ? t("servers.notInstalled") : "未安装", tone: "neutral" as const };
  }
  if (metrics[component] === true) {
    return { label: t ? t("state.ready") : "已就绪", tone: "success" as const };
  }
  if (component === "agent" && metrics.agent === false && metrics.ssh === true) {
    return { label: t?.("servers.agentNotRunning") ?? "未运行", tone: "warning" as const };
  }
  if (component === "traefik" && status === "swarm_manager_required") {
    return { label: t ? t("servers.notEnabled") : "未启用", tone: "neutral" as const };
  }
  return { label: t ? t("servers.notProbed") : "待探测", tone: "neutral" as const };
}

export function runtimeCheckOutcome(check: RuntimeCheck) {
  const optionalMissing = [
    "SWARM_NOT_INITIALIZED",
    "TRAEFIK_NOT_INSTALLED",
    "FRPS_NOT_INSTALLED",
    "JENKINS_NOT_INSTALLED",
  ];
  if (optionalMissing.includes(check.code ?? "") || optionalMissing.includes(check.summary ?? "")) {
    return "skipped";
  }
  const outcome = check.outcome ?? check.status ?? "unknown";
  if (outcome === "success") return "passed";
  if (outcome === "danger") return "failed";
  return outcome;
}

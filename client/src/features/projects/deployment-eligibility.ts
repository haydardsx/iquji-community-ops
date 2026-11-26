import type { DeploymentEligibility, Server, ServerRuntimeMetrics } from "@/contracts/types";
import { swarmIdentityVerified } from "@/features/servers/swarm-connection";

export function managerOptions(servers: Server[], t: (key: any) => string, routeCluster?: string) {
  return [
    { label: t("projects.serverNone"), value: "", disabled: false },
    ...servers.map((server) => {
      const metrics = (server.health?.metrics ?? {}) as ServerRuntimeMetrics;
      const cluster = metrics.swarmClusterId;
      const manager =
        swarmIdentityVerified(metrics) &&
        metrics.swarmRole === "manager" &&
        metrics.swarmControlAvailable === true &&
        Boolean(cluster);
      const routing = routeCluster !== undefined;
      const ready = manager && (!routing || (routeCluster === cluster && metrics.traefik === true));
      const reason = !manager
        ? t("projects.managerUnavailable")
        : routing && routeCluster !== cluster
          ? t("projects.routeClusterMismatch")
          : t("projects.traefikNotReady");
      return {
        label: [server.name, cluster, ready ? "" : reason].filter(Boolean).join(" · "),
        value: server.id,
        disabled: !ready,
      };
    }),
  ];
}

export function deploymentServerOptions(
  servers: Server[],
  eligibility: DeploymentEligibility | undefined,
  selectedIds: string[],
  allowedIds?: string[],
  t?: (key: any, params?: any) => string,
) {
  const results = new Map(eligibility?.servers.map((item) => [item.serverId, item]));
  const options = servers.map((server) => {
    const result = results.get(server.id);
    const inScope = !allowedIds?.length || allowedIds.includes(server.id);
    const eligible = result?.eligible === true && inScope;
    const reason = !inScope
      ? t
        ? t("projects.notInAllowedDeployScope")
        : "不在允许部署范围"
      : result?.reasons.map((item) => item.message).join("、") ||
        (t ? t("projects.waitingDeployCheck") : "等待部署资格检查");
    const selected = selectedIds.includes(server.id);
    const eligibleLabel = t ? t("projects.deployable") : "可部署";
    const selectedReason = t
      ? t("projects.selectedNotDeployable", { reason })
      : `已选但不可部署（${reason}）`;
    return {
      value: server.id,
      label: `${server.name} · ${eligible ? eligibleLabel : selected ? selectedReason : reason}`,
      disabled: !eligible,
      reason: eligible ? "" : reason,
    };
  });
  const existing = new Set(options.map((item) => item.value));
  for (const id of selectedIds) {
    if (id && !existing.has(id)) {
      const invalidLabel = t ? t("projects.invalidServerScope") : "已失效（不属于项目可用服务器）";
      const notBelong = t ? t("projects.notBelongToProject") : "不属于项目可用服务器";
      options.push({
        value: id,
        label: `${id} · ${invalidLabel}`,
        disabled: true,
        reason: notBelong,
      });
      existing.add(id);
    }
  }
  return options;
}

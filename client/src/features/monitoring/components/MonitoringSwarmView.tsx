import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiEmptyState,
  IqujiLoadingState,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useNavigate } from "@tanstack/react-router";
import { useCallback, useMemo, useState } from "react";
import { workspacePages } from "@/contracts/pages";
import type { SwarmNetworkRecord } from "@/features/swarm/components/SwarmNetworksTable";
import type { SwarmNodeRecord } from "@/features/swarm/components/SwarmNodesTable";
import type { SwarmServiceRecord } from "@/features/swarm/components/SwarmServicesTable";
import type { SwarmTaskRecord } from "@/features/swarm/components/SwarmTasksTable";
import { useI18n } from "@/shared/i18n";
import type { MonitoringServer, SwarmOverview } from "../api";
import { MonitoringSwarmNodeRow } from "./MonitoringSwarmNodeRow";

export function MonitoringSwarmView({
  isLoading,
  swarm,
  serverList = [],
  searchKeyword = "",
}: {
  isLoading: boolean;
  swarm: SwarmOverview;
  serverList?: MonitoringServer[];
  searchKeyword?: string;
}) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [copiedText, setCopiedText] = useState<string | null>(null);

  const handleCopy = useCallback(async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedText(text);
      setTimeout(() => setCopiedText(null), 2000);
    } catch {
      // 忽略剪贴板错误
    }
  }, []);

  const rawNodes = (swarm.nodes ?? []) as unknown as SwarmNodeRecord[];
  const rawServices = (swarm.services ?? []) as unknown as SwarmServiceRecord[];
  const rawTasks = (swarm.tasks ?? []) as unknown as SwarmTaskRecord[];
  const rawNetworks = (swarm.networks ?? []) as unknown as SwarmNetworkRecord[];

  const isNotEnabled = swarm.status === "not_enabled" && rawNodes.length === 0;
  const diagnostics = (swarm.snapshots ?? []).flatMap((snapshot) => {
    if (
      typeof snapshot.errorCode !== "string" ||
      !snapshot.errorCode ||
      (snapshot.status !== "error" && !snapshot.failureAt)
    )
      return [];
    const code = snapshot.errorCode;
    const key = `monitoring.${code}`;
    const reason = t(key);
    const server =
      serverList.find((server) => server.serverId === snapshot.serverId)?.serverName ||
      (typeof snapshot.serverName === "string" && snapshot.serverName) ||
      (typeof snapshot.serverId === "string" && snapshot.serverId);
    return [[server, reason === key ? code : `${reason} (${code})`].filter(Boolean).join(" · ")];
  });

  const resolveServerForNode = (node: SwarmNodeRecord) =>
    node.serverId
      ? serverList.find((server) => server.serverId === node.serverId)
      : node.id
        ? serverList.find((server) => server.swarmNodeId === node.id)
        : undefined;

  // 搜索过滤
  const kw = searchKeyword.trim().toLowerCase();

  const filteredNodes = useMemo(() => {
    if (!kw) return rawNodes;
    return rawNodes.filter(
      (n) =>
        n.hostname?.toLowerCase().includes(kw) ||
        n.id?.toLowerCase().includes(kw) ||
        n.role?.toLowerCase().includes(kw) ||
        n.serverId?.toLowerCase().includes(kw),
    );
  }, [rawNodes, kw]);

  const filteredServices = useMemo(() => {
    if (!kw) return rawServices;
    return rawServices.filter(
      (s) =>
        s.name?.toLowerCase().includes(kw) ||
        s.image?.toLowerCase().includes(kw) ||
        s.id?.toLowerCase().includes(kw),
    );
  }, [rawServices, kw]);

  const filteredTasks = useMemo(() => {
    if (!kw) return rawTasks;
    return rawTasks.filter(
      (t) =>
        t.serviceName?.toLowerCase().includes(kw) ||
        t.id?.toLowerCase().includes(kw) ||
        t.nodeId?.toLowerCase().includes(kw) ||
        t.currentState?.toLowerCase().includes(kw),
    );
  }, [rawTasks, kw]);

  const filteredNetworks = useMemo(() => {
    if (!kw) return rawNetworks;
    return rawNetworks.filter(
      (net) =>
        net.name?.toLowerCase().includes(kw) ||
        net.id?.toLowerCase().includes(kw) ||
        net.driver?.toLowerCase().includes(kw) ||
        net.scope?.toLowerCase().includes(kw),
    );
  }, [rawNetworks, kw]);

  // 控制面 (Manager) 与 算力计算面 (Worker) 架构分级与层级排序
  const { managerNodes, workerNodes } = useMemo(() => {
    const managers: SwarmNodeRecord[] = [];
    const workers: SwarmNodeRecord[] = [];

    for (const node of filteredNodes) {
      if (node.role?.toLowerCase() === "manager") {
        managers.push(node);
      } else {
        workers.push(node);
      }
    }

    // Leader 永远排在第 1 位
    managers.sort((a, b) => {
      const aLeader = a.managerStatus?.toLowerCase() === "leader" ? -1 : 1;
      const bLeader = b.managerStatus?.toLowerCase() === "leader" ? -1 : 1;
      return aLeader - bLeader;
    });

    return { managerNodes: managers, workerNodes: workers };
  }, [filteredNodes]);

  // 建立 Node 与 Task 的关联映射
  const tasksByNodeId = useMemo(() => {
    const map = new Map<string, SwarmTaskRecord[]>();
    for (const task of rawTasks) {
      if (!task.nodeId) continue;
      const list = map.get(task.nodeId) ?? [];
      list.push(task);
      map.set(task.nodeId, list);
    }
    return map;
  }, [rawTasks]);

  if (isLoading) {
    return (
      <div style={{ padding: 48 }}>
        <IqujiLoadingState label={t("common.loading")} />
      </div>
    );
  }

  if (isNotEnabled) {
    return (
      <div className="ops-swarm-empty-wrap">
        <IqujiEmptyState
          body={t("swarm.emptyBody")}
          icon={<IqujiIcon decorative name="container" size={40} />}
          title={t("swarm.emptyTitle")}
          action={
            <div className="ops-swarm-empty-action">
              <IqujiButton
                variant="primary"
                iconStart={<IqujiIcon decorative name="server" size={14} />}
                onClick={() => void navigate({ to: workspacePages.servers.path })}
              >
                {t("swarm.goToServers")}
              </IqujiButton>
            </div>
          }
        />
      </div>
    );
  }

  if (
    swarm.status !== "ready" &&
    !rawNodes.length &&
    !rawServices.length &&
    !rawTasks.length &&
    !rawNetworks.length
  ) {
    const status =
      swarm.status === "error"
        ? "swarm.statusError"
        : swarm.status === "stale"
          ? "swarm.statusStale"
          : "swarm.statusUnverified";
    return <IqujiEmptyState title={t(status)} body={diagnostics.join("；") || undefined} />;
  }

  const hasSearchFilter = Boolean(kw);
  const totalFilteredCount =
    filteredNodes.length + filteredServices.length + filteredTasks.length + filteredNetworks.length;

  return (
    <div className="ops-swarm-flat-canvas">
      {diagnostics.length > 0 && (
        <IqujiAlert tone="warning" title={t("monitoring.swarmError")}>
          {diagnostics.join("；")}
        </IqujiAlert>
      )}
      {/* 搜索无结果时 */}
      {hasSearchFilter && totalFilteredCount === 0 ? (
        <div style={{ padding: "32px 0" }}>
          <IqujiEmptyState
            body={t("common.noData")}
            icon={<IqujiIcon decorative name="search" size={32} />}
            title={t("monitoring.emptySearch")}
          />
        </div>
      ) : null}

      <div className="ops-swarm-node-zone">
        <div className="ops-swarm-zone-bar">
          <div className="ops-swarm-zone-bar__left">
            <span className="ops-swarm-zone-bar__title">{t("swarm.tabNodes")}</span>
            <span className="ops-swarm-zone-bar__pill">{filteredNodes.length}</span>
          </div>
        </div>

        <div className="ops-swarm-table-scroll">
          <table className="ops-swarm-flat-table">
            <thead>
              <tr>
                <th style={{ width: "32%" }}>{t("monitoring.colServerNode")}</th>
                <th style={{ width: "18%" }}>
                  {t("swarm.colRole")} / {t("swarm.colNetwork")}
                </th>
                <th style={{ width: "14%" }}>{t("swarm.colStatus")}</th>
                <th style={{ width: "12%" }}>{t("swarm.colEngine")}</th>
                <th style={{ width: "24%" }}>{t("monitoring.colTasks")}</th>
              </tr>
            </thead>
            <tbody>
              {filteredNodes.length === 0 ? (
                <tr>
                  <td colSpan={5} className="ops-table-empty-cell">
                    {t("common.noData")}
                  </td>
                </tr>
              ) : (
                <>
                  {managerNodes.map((node) => (
                    <MonitoringSwarmNodeRow
                      key={node.id || node.hostname}
                      node={node}
                      isWorker={false}
                      isLastWorker={false}
                      linkedServer={resolveServerForNode(node)}
                      nodeTasks={node.id ? (tasksByNodeId.get(node.id) ?? []) : []}
                    />
                  ))}
                  {workerNodes.map((node, idx) => (
                    <MonitoringSwarmNodeRow
                      key={node.id || node.hostname}
                      node={node}
                      isWorker={node.role === "worker"}
                      isLastWorker={idx === workerNodes.length - 1}
                      linkedServer={resolveServerForNode(node)}
                      nodeTasks={node.id ? (tasksByNodeId.get(node.id) ?? []) : []}
                    />
                  ))}
                </>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="ops-swarm-zone-separator" />

      <div className="ops-swarm-bottom-split">
        {/* 左侧：编排服务与调度任务 (Services & Tasks) */}
        <div className="ops-swarm-split-col">
          <div className="ops-swarm-zone-bar">
            <div className="ops-swarm-zone-bar__left">
              <span className="ops-swarm-zone-bar__title">{t("swarm.tabServices")}</span>
              <span className="ops-swarm-zone-bar__pill">{filteredServices.length}</span>
            </div>
          </div>

          <div className="ops-swarm-table-scroll">
            {filteredServices.length === 0 ? (
              <div className="ops-swarm-empty-block">
                <IqujiIcon decorative name="boxes" size={24} />
                <p className="ops-swarm-empty-block__title">{t("swarm.noServicesTitle")}</p>
                <p className="ops-swarm-empty-block__desc">{t("swarm.noServicesDesc")}</p>
              </div>
            ) : (
              <table className="ops-swarm-flat-table">
                <thead>
                  <tr>
                    <th style={{ width: "38%" }}>{t("swarm.colServiceName")}</th>
                    <th style={{ width: "36%" }}>{t("swarm.colImage")}</th>
                    <th style={{ width: "26%" }}>{t("swarm.colReplicas")}</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredServices.map((service) => (
                    <tr key={service.id || service.name}>
                      <td>
                        <div className="ops-service-cell">
                          <span className="ops-service-name">{service.name || "—"}</span>
                          {service.id ? (
                            <span className="ops-service-id-sub">
                              ID: {service.id.slice(0, 10)}
                            </span>
                          ) : null}
                        </div>
                      </td>
                      <td>
                        <div className="ops-service-image-cell">
                          <span className="ops-service-image-text" title={service.image}>
                            {service.image || "—"}
                          </span>
                          {service.image ? (
                            <IqujiButton
                              compact
                              variant="ghost"
                              type="button"
                              className="ops-copy-mini-btn"
                              onClick={() => handleCopy(service.image ?? "")}
                              title={
                                copiedText === service.image
                                  ? t("common.copied")
                                  : t("monitoring.copyImage")
                              }
                            >
                              <IqujiIcon
                                decorative
                                name={copiedText === service.image ? "check" : "copy"}
                                size={11}
                              />
                            </IqujiButton>
                          ) : null}
                        </div>
                      </td>
                      <td>
                        <div className="ops-service-replicas-cell">
                          <IqujiBadge tone="info">
                            {service.replicas
                              ? t("monitoring.replicasCount", { count: service.replicas })
                              : t("common.unknown")}
                          </IqujiBadge>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>

        {/* 右侧：覆盖网络 (Overlay Networks) */}
        <div className="ops-swarm-split-col">
          <div className="ops-swarm-zone-bar">
            <div className="ops-swarm-zone-bar__left">
              <span className="ops-swarm-zone-bar__title">{t("swarm.tabNetworks")}</span>
              <span className="ops-swarm-zone-bar__pill">{filteredNetworks.length}</span>
            </div>
          </div>

          <div className="ops-swarm-table-scroll">
            {filteredNetworks.length === 0 ? (
              <div className="ops-swarm-empty-block">
                <IqujiIcon decorative name="network" size={24} />
                <p className="ops-swarm-empty-block__title">{t("monitoring.noNetworksTitle")}</p>
                <p className="ops-swarm-empty-block__desc">{t("monitoring.noNetworksDesc")}</p>
              </div>
            ) : (
              <table className="ops-swarm-flat-table">
                <thead>
                  <tr>
                    <th style={{ width: "36%" }}>{t("swarm.colNetworkName")}</th>
                    <th style={{ width: "32%" }}>{t("swarm.colDriverScope")}</th>
                    <th style={{ width: "32%" }}>{t("swarm.colNetworkId")}</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredNetworks.map((net) => (
                    <tr key={net.id || net.name}>
                      <td>
                        <div className="ops-swarm-net-name-cell">
                          <span className="ops-status-dot" data-tone="success" />
                          <span className="ops-swarm-net-name">{net.name || "—"}</span>
                        </div>
                      </td>
                      <td>
                        <div className="ops-swarm-net-badges">
                          <IqujiBadge tone="info">{net.driver || "overlay"}</IqujiBadge>
                          <IqujiBadge tone="neutral">{net.scope || "swarm"}</IqujiBadge>
                        </div>
                      </td>
                      <td>
                        <div className="ops-swarm-net-id-cell">
                          <span className="ops-swarm-mono-text">
                            {net.id ? net.id.slice(0, 10) : "—"}
                          </span>
                          {net.id ? (
                            <IqujiButton
                              compact
                              variant="ghost"
                              type="button"
                              className="ops-copy-mini-btn"
                              onClick={() => handleCopy(net.id ?? "")}
                              title={
                                copiedText === net.id
                                  ? t("common.copied")
                                  : t("monitoring.copyNetworkId")
                              }
                            >
                              <IqujiIcon
                                decorative
                                name={copiedText === net.id ? "check" : "copy"}
                                size={11}
                              />
                            </IqujiButton>
                          ) : null}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

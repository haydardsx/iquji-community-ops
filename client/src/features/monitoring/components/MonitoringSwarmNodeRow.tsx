import { IqujiBadge, IqujiButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useNavigate } from "@tanstack/react-router";
import { useCallback, useState } from "react";
import { workspacePages } from "@/contracts/pages";
import { swarmSampleConnectionFacts } from "@/features/servers/swarm-connection";
import type { SwarmNodeRecord } from "@/features/swarm/components/SwarmNodesTable";
import type { SwarmTaskRecord } from "@/features/swarm/components/SwarmTasksTable";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import type { MonitoringServer } from "../api";

export interface MonitoringSwarmNodeRowProps {
  node: SwarmNodeRecord;
  isWorker: boolean;
  isLastWorker: boolean;
  linkedServer?: MonitoringServer;
  nodeTasks: SwarmTaskRecord[];
}

export function MonitoringSwarmNodeRow({
  node,
  isWorker,
  isLastWorker,
  linkedServer,
  nodeTasks,
}: MonitoringSwarmNodeRowProps) {
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

  const isManager = node.role === "manager";
  const isStale = node.sampleStatus === "stale";
  const isUnverified = !isStale && node.sampleStatus !== "fresh";
  const isLeader = node.managerStatus?.toLowerCase() === "leader";
  const isReady = node.status?.toLowerCase() === "ready";
  const isDown = node.status?.toLowerCase() === "down";
  const isActive = node.availability?.toLowerCase() === "active";

  const connection = swarmSampleConnectionFacts(
    isStale || isUnverified
      ? {}
      : {
          swarmRole: node.role,
          swarmConnectionMode: node.connectionMode,
          swarmAdvertiseAddr: node.advertiseAddr,
          swarmManagerAddress: node.managerAddress,
        },
    t,
  );

  return (
    <tr
      key={node.id || node.hostname}
      className={
        isManager
          ? "ops-swarm-node-row ops-swarm-node-row--manager"
          : "ops-swarm-node-row ops-swarm-node-row--worker"
      }
    >
      <td>
        <div className="ops-node-server-cell">
          <div className="ops-node-server-title-row">
            {isWorker && !isStale && !isUnverified ? (
              <span
                className="ops-swarm-tree-connector"
                aria-hidden="true"
                title={t("monitoring.managedByManager")}
              >
                {isLastWorker ? "└─" : "├─"}
              </span>
            ) : null}
            <span className="ops-node-server-name">
              {linkedServer
                ? linkedServer.serverName
                : node.serverName || t("monitoring.externalNode")}
            </span>
            <IqujiBadge tone={linkedServer ? "info" : "neutral"}>
              {linkedServer ? t("monitoring.managedHost") : t("monitoring.externalNode")}
            </IqujiBadge>
            {linkedServer ? (
              <IqujiButton
                compact
                variant="ghost"
                type="button"
                className="ops-node-link-btn"
                onClick={() =>
                  void navigate({
                    to: workspacePages.servers.path,
                    search: { serverId: linkedServer.serverId },
                  })
                }
                title={t("monitoring.viewServerTitle")}
              >
                {t("monitoring.viewServer")}
                <OperationActivityDot scope={{ serverId: linkedServer.serverId }} />
              </IqujiButton>
            ) : null}
          </div>
          <div className="ops-node-server-sub-row">
            {linkedServer?.publicHost || node.publicHost ? (
              <span className="ops-node-server-ip">
                {t("monitoring.ip")} {linkedServer?.publicHost || node.publicHost}
              </span>
            ) : null}
            <span className="ops-node-hostname-tag">
              {t("monitoring.hostname")} {node.hostname || "—"}
            </span>
            <span className="ops-node-id-group">
              <span className="ops-node-id-tag">
                {t("monitoring.nodeId")} {node.id ? node.id.slice(0, 10) : "—"}
              </span>
              {node.id ? (
                <IqujiButton
                  compact
                  variant="ghost"
                  type="button"
                  className="ops-copy-mini-btn"
                  onClick={() => void handleCopy(node.id ?? "")}
                  title={copiedText === node.id ? t("common.copied") : t("monitoring.copyNodeId")}
                >
                  <IqujiIcon
                    decorative
                    name={copiedText === node.id ? "check" : "copy"}
                    size={11}
                  />
                </IqujiButton>
              ) : null}
            </span>
          </div>
        </div>
      </td>
      <td>
        <div className="ops-node-role-cell">
          <IqujiBadge tone={isManager && !isStale && !isUnverified ? "info" : "neutral"}>
            {isStale || isUnverified
              ? t("swarm.statusUnverified")
              : node.role
                ? node.role.charAt(0).toUpperCase() + node.role.slice(1)
                : t("common.unknown")}
          </IqujiBadge>
          {isLeader && !isStale && !isUnverified ? (
            <span className="ops-swarm-leader-badge" title={t("swarm.leader")}>
              ⭐ Leader
            </span>
          ) : null}
        </div>
        <div
          className="ops-swarm-mono-sub"
          style={{ display: "block", overflowWrap: "anywhere", marginTop: 2 }}
          title={t("swarm.colConnection")}
        >
          {connection.label}
        </div>
      </td>
      <td>
        <div className="ops-node-status-cell">
          <span
            className="ops-status-dot"
            data-tone={
              isStale
                ? "warning"
                : isUnverified
                  ? "neutral"
                  : isReady && isActive
                    ? "success"
                    : isReady || isDown
                      ? "warning"
                      : "neutral"
            }
          />
          <span className="ops-node-status-text">
            {isStale
              ? t("swarm.statusStale")
              : isUnverified
                ? t("swarm.statusUnverified")
                : isReady
                  ? t("swarm.statusReady")
                  : isDown
                    ? t("swarm.statusDown")
                    : t("common.unknown")}
            {!isStale && !isUnverified && node.availability ? ` (${node.availability})` : ""}
          </span>
        </div>
      </td>
      <td>
        <span className="ops-swarm-mono-text">
          {node.engineVersion ? `v${node.engineVersion}` : "—"}
        </span>
      </td>
      <td>
        <div className="ops-node-tasks-cell">
          {nodeTasks.length === 0 ? (
            <span className="ops-muted-hint">{t("monitoring.noTasks")}</span>
          ) : (
            <div className="ops-node-tasks-flow">
              {nodeTasks.map((task) => {
                const isRunning = task.currentState?.toLowerCase() === "running";
                return (
                  <span
                    key={task.id}
                    className="ops-node-task-chip"
                    data-tone={isRunning ? "success" : "warning"}
                    title={`${task.serviceName || "task"} · ID: ${task.id?.slice(0, 10)}`}
                  >
                    <span
                      className="ops-status-dot"
                      data-tone={isRunning ? "success" : "warning"}
                    />
                    <span className="ops-node-task-chip__name">
                      {task.serviceName || task.id?.slice(0, 8)}
                    </span>
                    <span className="ops-node-task-chip__state">
                      {task.currentState || "unknown"}
                    </span>
                  </span>
                );
              })}
            </div>
          )}
        </div>
      </td>
    </tr>
  );
}

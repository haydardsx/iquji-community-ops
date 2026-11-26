import { IqujiButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import type { SystemHealthSummary } from "../monitoring-utils";

export type MonitoringTabKey = "projects" | "servers" | "swarm";

export function MonitoringStatCards({
  activeTab,
  onSelectTab,
  summary,
}: {
  activeTab: MonitoringTabKey;
  onSelectTab: (tab: MonitoringTabKey) => void;
  summary: SystemHealthSummary;
}) {
  const { t } = useI18n();

  const allAgentsOnline = summary.totalAgents > 0 && summary.onlineAgents === summary.totalAgents;
  const offlineAgents = summary.offlineAgents;
  const hasAgentIssues = offlineAgents > 0 || summary.staleAgents > 0 || summary.unknownAgents > 0;
  const hasServerIssues = summary.offlineServers > 0 || summary.unknownServers > 0;
  const hasServerOrProbeIssue = hasServerIssues || hasAgentIssues;

  const serverTone = hasServerOrProbeIssue
    ? "warning"
    : summary.onlineServers > 0
      ? "success"
      : "neutral";

  return (
    <div className="ops-stat-grid ops-stat-grid--triple">
      <IqujiButton
        variant="ghost"
        type="button"
        className={`ops-stat-modern ${activeTab === "projects" ? "ops-stat-modern--active" : ""}`}
        data-ops-guide-tab="projects"
        onClick={() => onSelectTab("projects")}
      >
        <div className="ops-stat-modern__head">
          <span className="ops-stat-modern__title">
            {t("monitoring.servicesTitle")}
            <OperationActivityDot scope={{ navigation: "projects" }} />
          </span>
          <span className="ops-stat-modern__icon" data-color="blue">
            <IqujiIcon decorative name="boxes" size={16} />
          </span>
        </div>
        <div className="ops-stat-modern__value-row">
          <span className="ops-stat-modern__number">{summary.runningProjects}</span>
          <span className="ops-stat-modern__unit">/ {summary.totalProjects}</span>
        </div>
        <div className="ops-stat-modern__footer">
          <span
            className="ops-status-dot"
            data-tone={summary.runningProjects > 0 ? "success" : "neutral"}
          />
          <span className="ops-stat-modern__desc">
            {summary.runningProjects} {t("state.running")} · {summary.idleProjects}{" "}
            {t("state.idle")}
          </span>
        </div>
      </IqujiButton>

      <IqujiButton
        variant="ghost"
        type="button"
        className={`ops-stat-modern ${activeTab === "servers" ? "ops-stat-modern--active" : ""}`}
        data-ops-guide-tab="servers"
        onClick={() => onSelectTab("servers")}
      >
        <div className="ops-stat-modern__head">
          <span className="ops-stat-modern__title">
            {t("monitoring.serversAndProbesTitle")}
            <OperationActivityDot scope={{ navigation: "servers" }} />
          </span>
          <span
            className="ops-stat-modern__icon"
            data-color={hasServerOrProbeIssue ? "amber" : "emerald"}
          >
            <IqujiIcon decorative name="server" size={16} />
          </span>
        </div>
        <div className="ops-stat-modern__value-row">
          <span className="ops-stat-modern__number">{summary.onlineServers}</span>
          <span className="ops-stat-modern__unit">
            / {summary.totalServers} {t("common.online")}
          </span>
        </div>
        <div className="ops-stat-modern__footer">
          <span
            className={`ops-status-dot ${!hasServerOrProbeIssue && summary.onlineServers > 0 ? "ops-status-dot--pulse" : ""}`}
            data-tone={serverTone}
          />
          <span className="ops-stat-modern__desc">
            {summary.unknownServers > 0
              ? t("monitoring.serversUnverifiedSummary", {
                  unknown: summary.unknownServers,
                  offline: summary.offlineServers,
                  agentOnline: summary.onlineAgents,
                  agentTotal: summary.totalAgents,
                })
              : summary.offlineServers > 0
                ? t("monitoring.serversOfflineSummary", {
                    online: summary.onlineServers,
                    offline: summary.offlineServers,
                    agentOnline: summary.onlineAgents,
                    agentTotal: summary.totalAgents,
                  })
                : summary.totalServers === 0
                  ? t("common.noData")
                  : allAgentsOnline && summary.onlineServers === summary.totalServers
                    ? t("monitoring.allServersProbesReady")
                    : t("monitoring.serversOnlineSummary", {
                        online: summary.onlineServers,
                        agentOnline: summary.onlineAgents,
                        agentTotal: summary.totalAgents,
                      })}
          </span>
        </div>
      </IqujiButton>

      <IqujiButton
        variant="ghost"
        type="button"
        className={`ops-stat-modern ${activeTab === "swarm" ? "ops-stat-modern--active" : ""}`}
        data-ops-guide-tab="swarm"
        onClick={() => onSelectTab("swarm")}
      >
        <div className="ops-stat-modern__head">
          <span className="ops-stat-modern__title">
            {t("monitoring.swarmCluster")}
            <OperationActivityDot scope={{ action: "docker_swarm" }} />
          </span>
          <span className="ops-stat-modern__icon" data-color="indigo">
            <IqujiIcon decorative name="container" size={16} />
          </span>
        </div>
        <div className="ops-stat-modern__value-row">
          <span className="ops-stat-modern__number">{summary.swarmClusterCount ?? "—"}</span>
          <span className="ops-stat-modern__unit">
            {t(
              summary.swarmClusterCount === 1 ? "monitoring.oneCluster" : "monitoring.clusterCount",
            )}
          </span>
        </div>
        <div className="ops-stat-modern__footer">
          <span
            className="ops-status-dot"
            data-tone={
              summary.swarmStatus === "ready"
                ? "success"
                : summary.swarmStatus === "not_enabled"
                  ? "neutral"
                  : "warning"
            }
          />
          <span className="ops-stat-modern__desc">
            {summary.swarmStatus === "ready"
              ? t("monitoring.swarmNodeRoles", {
                  nodes: summary.swarmNodeCount,
                  managers: summary.swarmManagerCount,
                  workers: summary.swarmWorkerCount,
                })
              : t(
                  summary.swarmStatus === "stale"
                    ? "monitoring.swarmStale"
                    : summary.swarmStatus === "unverified"
                      ? "monitoring.swarmUnverified"
                      : summary.swarmStatus === "error"
                        ? "monitoring.swarmError"
                        : "monitoring.swarmNotEnabled",
                )}
          </span>
        </div>
      </IqujiButton>
    </div>
  );
}

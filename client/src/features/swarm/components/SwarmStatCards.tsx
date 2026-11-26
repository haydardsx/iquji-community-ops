import { IqujiButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import type { SwarmOverview } from "@/features/monitoring/api";
import { useI18n } from "@/shared/i18n";

export type SwarmTabKey = "nodes" | "services" | "tasks" | "networks";

export function SwarmStatCards({
  activeTab,
  onSelectTab,
  swarm,
}: {
  activeTab: SwarmTabKey;
  onSelectTab: (tab: SwarmTabKey) => void;
  swarm: SwarmOverview;
}) {
  const { t } = useI18n();

  const nodes = swarm.nodes ?? [];
  const services = swarm.services ?? [];
  const tasks = swarm.tasks ?? [];
  const clusterCount = swarm.clusterCount ?? (swarm.clusters?.length || 0);

  const managerCount = nodes.filter((n) => n.role === "manager").length;
  const workerCount = nodes.filter((n) => n.role === "worker").length;

  const runningTasks = tasks.filter((task) => {
    const cs = typeof task.currentState === "string" ? task.currentState : "";
    return cs.toLowerCase().includes("running");
  }).length;

  const abnormalTasks = tasks.filter((task) => {
    const cs = typeof task.currentState === "string" ? task.currentState : "";
    return (
      Boolean(task.error) ||
      ["failed", "rejected", "shutdown"].some((s) => cs.toLowerCase().includes(s))
    );
  }).length;

  const statusTone =
    swarm.status === "ready" ? "success" : swarm.status === "not_enabled" ? "neutral" : "warning";

  const statusLabel = t(
    swarm.status === "ready"
      ? "swarm.statusReady"
      : swarm.status === "stale"
        ? "swarm.statusStale"
        : swarm.status === "unverified"
          ? "swarm.statusUnverified"
          : swarm.status === "error"
            ? "swarm.statusError"
            : "swarm.statusNotEnabled",
  );

  return (
    <div className="ops-stat-grid">
      {/* 1. 集群健康 */}
      <IqujiButton
        variant="ghost"
        type="button"
        className={`ops-stat-modern ${activeTab === "nodes" ? "ops-stat-modern--active" : ""}`}
        onClick={() => onSelectTab("nodes")}
      >
        <div className="ops-stat-modern__head">
          <span className="ops-stat-modern__title">{t("swarm.statClusterStatus")}</span>
          <span className="ops-stat-modern__icon" data-color="indigo">
            <IqujiIcon decorative name="container" size={16} />
          </span>
        </div>
        <div className="ops-stat-modern__value-row">
          <span className="ops-stat-modern__number">{clusterCount}</span>
          <span className="ops-stat-modern__unit">{t("monitoring.clusterCount")}</span>
        </div>
        <div className="ops-stat-modern__footer">
          <span
            className={`ops-status-dot ${swarm.status === "ready" ? "ops-status-dot--pulse" : ""}`}
            data-tone={statusTone}
          />
          <span className="ops-stat-modern__desc">
            {clusterCount > 0
              ? t("swarm.activeClusterSummary", { count: clusterCount, status: statusLabel })
              : statusLabel}
          </span>
        </div>
      </IqujiButton>

      {/* 2. 节点规模 */}
      <IqujiButton
        variant="ghost"
        type="button"
        className={`ops-stat-modern ${activeTab === "nodes" ? "ops-stat-modern--active" : ""}`}
        onClick={() => onSelectTab("nodes")}
      >
        <div className="ops-stat-modern__head">
          <span className="ops-stat-modern__title">{t("swarm.statNodes")}</span>
          <span className="ops-stat-modern__icon" data-color="blue">
            <IqujiIcon decorative name="server" size={16} />
          </span>
        </div>
        <div className="ops-stat-modern__value-row">
          <span className="ops-stat-modern__number">{nodes.length}</span>
          <span className="ops-stat-modern__unit">{t("monitoring.nodeCount")}</span>
        </div>
        <div className="ops-stat-modern__footer">
          <span className="ops-status-dot" data-tone={nodes.length > 0 ? "success" : "neutral"} />
          <span className="ops-stat-modern__desc">
            {nodes.length > 0
              ? `${managerCount} manager · ${workerCount} worker`
              : t("common.noData")}
          </span>
        </div>
      </IqujiButton>

      {/* 3. 编排服务 */}
      <IqujiButton
        variant="ghost"
        type="button"
        className={`ops-stat-modern ${activeTab === "services" ? "ops-stat-modern--active" : ""}`}
        onClick={() => onSelectTab("services")}
      >
        <div className="ops-stat-modern__head">
          <span className="ops-stat-modern__title">{t("swarm.statServices")}</span>
          <span className="ops-stat-modern__icon" data-color="emerald">
            <IqujiIcon decorative name="boxes" size={16} />
          </span>
        </div>
        <div className="ops-stat-modern__value-row">
          <span className="ops-stat-modern__number">{services.length}</span>
          <span className="ops-stat-modern__unit">{t("monitoring.serviceCount")}</span>
        </div>
        <div className="ops-stat-modern__footer">
          <span
            className="ops-status-dot"
            data-tone={services.length > 0 ? "success" : "neutral"}
          />
          <span className="ops-stat-modern__desc">
            {services.length > 0
              ? t("swarm.runningTasksSummary", { count: runningTasks })
              : t("common.noData")}
          </span>
        </div>
      </IqujiButton>

      {/* 4. 调度任务 */}
      <IqujiButton
        variant="ghost"
        type="button"
        className={`ops-stat-modern ${activeTab === "tasks" ? "ops-stat-modern--active" : ""}`}
        onClick={() => onSelectTab("tasks")}
      >
        <div className="ops-stat-modern__head">
          <span className="ops-stat-modern__title">{t("swarm.statTasks")}</span>
          <span
            className="ops-stat-modern__icon"
            data-color={abnormalTasks > 0 ? "amber" : "emerald"}
          >
            <IqujiIcon decorative name="radar" size={16} />
          </span>
        </div>
        <div className="ops-stat-modern__value-row">
          <span className="ops-stat-modern__number">{tasks.length}</span>
          <span className="ops-stat-modern__unit">{t("monitoring.taskCount")}</span>
        </div>
        <div className="ops-stat-modern__footer">
          <span
            className="ops-status-dot"
            data-tone={abnormalTasks > 0 ? "warning" : tasks.length > 0 ? "success" : "neutral"}
          />
          <span className="ops-stat-modern__desc">
            {abnormalTasks > 0
              ? t("swarm.abnormalTasksSummary", { count: abnormalTasks })
              : tasks.length > 0
                ? t("swarm.allTasksNormal")
                : t("common.noData")}
          </span>
        </div>
      </IqujiButton>
    </div>
  );
}

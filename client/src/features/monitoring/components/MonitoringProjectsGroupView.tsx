import {
  IqujiBadge,
  IqujiButton,
  IqujiEmptyState,
  IqujiLoadingState,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";
import { workspacePages } from "@/contracts/pages";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import type { MonitoringProject } from "../api";

export type ProjectGroup = {
  projectId: string;
  projectName: string;
  applications: MonitoringProject[];
  runningCount: number;
  idleCount: number;
  abnormalCount: number;
  totalContainers: number;
  totalAlerts: number;
};

export function MonitoringProjectsGroupView({
  isLoading,
  items,
  onlyAbnormal,
  searchKeyword,
}: {
  items: MonitoringProject[];
  isLoading: boolean;
  searchKeyword: string;
  onlyAbnormal: boolean;
}) {
  const navigate = useNavigate();
  const { t } = useI18n();

  // 按 projectId 聚合分组
  const projectGroups = useMemo<ProjectGroup[]>(() => {
    const map = new Map<string, ProjectGroup>();

    for (const item of items) {
      const pid = item.projectId || "unknown";
      let group = map.get(pid);
      if (!group) {
        group = {
          projectId: pid,
          projectName: item.projectName || pid,
          applications: [],
          runningCount: 0,
          idleCount: 0,
          abnormalCount: 0,
          totalContainers: 0,
          totalAlerts: 0,
        };
        map.set(pid, group);
      }

      group.applications.push(item);
      group.totalContainers += item.runningContainers || 0;
      if (item.alerts && item.alerts.length > 0) {
        group.totalAlerts += item.alerts.length;
      }

      const isRunning =
        item.status === "running" ||
        item.status === "success" ||
        item.status === "online" ||
        item.status === "ready" ||
        item.status === "succeeded";
      const isFailed =
        item.status === "failed" || item.status === "danger" || item.status === "error";

      if (isRunning) {
        group.runningCount++;
      } else if (isFailed) {
        group.abnormalCount++;
      } else {
        group.idleCount++;
      }
    }

    return Array.from(map.values());
  }, [items]);

  // 过滤
  const filteredGroups = useMemo(() => {
    const kw = searchKeyword.trim().toLowerCase();

    return projectGroups
      .map((group) => {
        // 过滤应用列表
        const matchedApps = group.applications.filter((app) => {
          if (onlyAbnormal) {
            const hasAlerts = app.alerts && app.alerts.length > 0;
            const isAbnormal =
              app.status === "failed" || app.status === "danger" || app.status === "error";
            if (!hasAlerts && !isAbnormal) return false;
          }
          if (kw) {
            const pMatch = group.projectName.toLowerCase().includes(kw);
            const aMatch = app.applicationName.toLowerCase().includes(kw);
            return pMatch || aMatch;
          }
          return true;
        });

        if (matchedApps.length === 0) return null;

        return {
          ...group,
          applications: matchedApps,
        };
      })
      .filter((g): g is ProjectGroup => g !== null);
  }, [projectGroups, onlyAbnormal, searchKeyword]);

  if (isLoading) {
    return <IqujiLoadingState label={t("monitoring.loadingProjects")} />;
  }

  if (filteredGroups.length === 0) {
    return (
      <div style={{ padding: 24 }}>
        <IqujiEmptyState
          body={
            searchKeyword || onlyAbnormal
              ? t("monitoring.noMatchingProjectsBody")
              : t("monitoring.noProjectsBody")
          }
          icon={<IqujiIcon decorative name="folder" />}
          title={
            searchKeyword || onlyAbnormal
              ? t("monitoring.noMatchingProjectsTitle")
              : t("monitoring.noProjectsTitle")
          }
        />
      </div>
    );
  }

  return (
    <div className="ops-project-groups-container">
      {filteredGroups.map((group) => (
        <section className="ops-project-group-card" key={group.projectId}>
          {/* 项目卡片头部：项目名称 + 统计微标 + 管理入口 */}
          <header className="ops-project-group-card__header">
            <div className="ops-project-group-card__header-left">
              <span className="ops-project-group-card__folder-icon">
                <IqujiIcon decorative name="folder" size={16} />
              </span>
              <IqujiButton
                variant="ghost"
                type="button"
                className="ops-project-group-card__title-btn"
                onClick={() =>
                  navigate({
                    to: workspacePages.projects.path,
                    search: { projectId: group.projectId },
                  })
                }
              >
                {group.projectName}
                <OperationActivityDot scope={{ projectId: group.projectId }} />
              </IqujiButton>

              <div className="ops-project-group-card__meta-pills">
                <span className="ops-project-meta-pill">
                  {t("monitoring.appCount", { count: group.applications.length })}
                </span>
                <span className="ops-project-meta-pill">
                  <span
                    className="ops-status-dot"
                    data-tone={group.runningCount > 0 ? "success" : "neutral"}
                  />
                  {t("monitoring.runningCount", { count: group.runningCount })}
                </span>
                <span className="ops-project-meta-pill">
                  {t("monitoring.idleCount", { count: group.idleCount })}
                </span>
                {group.abnormalCount > 0 ? (
                  <span className="ops-project-meta-pill" data-tone="danger">
                    <span className="ops-status-dot" data-tone="danger" />
                    {t("monitoring.abnormalCount", { count: group.abnormalCount })}
                  </span>
                ) : null}
              </div>
            </div>

            <div className="ops-project-group-card__header-right">
              {group.totalAlerts > 0 ? (
                <IqujiBadge tone="danger">
                  {t("monitoring.alertCount", { count: group.totalAlerts })}
                </IqujiBadge>
              ) : null}

              <IqujiButton
                compact
                iconEnd={<IqujiIcon decorative name="arrow-right" size={13} />}
                onClick={() =>
                  navigate({
                    to: workspacePages.projects.path,
                    search: { projectId: group.projectId },
                  })
                }
                variant="ghost"
              >
                {t("monitoring.manageProject")}
              </IqujiButton>
            </div>
          </header>

          {/* 下属微服务应用表格 */}
          <div className="ops-project-group-card__body">
            <table className="ops-project-apps-table">
              <thead>
                <tr>
                  <th style={{ width: "32%" }}>{t("monitoring.colApp")}</th>
                  <th style={{ width: "22%" }}>{t("monitoring.colStatus")}</th>
                  <th style={{ width: "18%" }}>{t("monitoring.colContainers")}</th>
                  <th style={{ width: "16%" }}>{t("monitoring.colHealth")}</th>
                  <th style={{ width: "12%", textAlign: "right" }}>{t("common.actions")}</th>
                </tr>
              </thead>
              <tbody>
                {group.applications.map((app) => {
                  const isRunning =
                    app.status === "running" ||
                    app.status === "success" ||
                    app.status === "online" ||
                    app.status === "ready" ||
                    app.status === "succeeded";
                  const isFailed =
                    app.status === "failed" || app.status === "danger" || app.status === "error";
                  const tone = isRunning ? "success" : isFailed ? "danger" : "neutral";
                  const label = isRunning
                    ? t("monitoring.statusRunning")
                    : isFailed
                      ? t("monitoring.statusError")
                      : app.status === "idle"
                        ? t("monitoring.statusIdle")
                        : app.status
                          ? t(`state.${app.status}` as any) || app.status
                          : t("monitoring.statusIdle");
                  const alerts = app.alerts ?? [];

                  return (
                    <tr key={app.applicationId}>
                      {/* 应用名称 */}
                      <td>
                        <IqujiButton
                          variant="ghost"
                          type="button"
                          className="ops-table-link-btn"
                          onClick={() =>
                            navigate({
                              to: workspacePages.projects.path,
                              search: {
                                projectId: app.projectId,
                                applicationId: app.applicationId,
                              },
                            })
                          }
                        >
                          {app.applicationName}
                          <OperationActivityDot scope={{ applicationId: app.applicationId }} />
                        </IqujiButton>
                      </td>

                      {/* 运行状态 */}
                      <td>
                        <div style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
                          <span
                            className={`ops-status-dot ${isRunning ? "ops-status-dot--pulse" : ""}`}
                            data-tone={tone}
                          />
                          <IqujiBadge tone={tone}>{label}</IqujiBadge>
                        </div>
                      </td>

                      {/* 容器实例 */}
                      <td>
                        <span style={{ fontFamily: "var(--iquji-font-mono, monospace)" }}>
                          {t("monitoring.instanceCount", { count: app.runningContainers })}
                        </span>
                      </td>

                      {/* 健康度/告警 */}
                      <td>
                        {alerts.length === 0 ? (
                          <span
                            style={{
                              display: "inline-flex",
                              alignItems: "center",
                              gap: 4,
                              color: "#10b981",
                              fontSize: 12,
                            }}
                          >
                            <IqujiIcon decorative name="shield" size={14} />
                            <span>{t("monitoring.healthy")}</span>
                          </span>
                        ) : (
                          <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
                            {alerts.map((alert) => (
                              <IqujiBadge
                                key={alert.code ?? alert.summary ?? alert.level}
                                tone="danger"
                              >
                                {alert.summary ?? alert.code ?? alert.level}
                              </IqujiBadge>
                            ))}
                          </div>
                        )}
                      </td>

                      {/* 操作 */}
                      <td style={{ textAlign: "right" }}>
                        <IqujiButton
                          compact
                          iconEnd={<IqujiIcon decorative name="arrow-right" size={12} />}
                          onClick={() =>
                            navigate({
                              to: workspacePages.projects.path,
                              search: {
                                projectId: app.projectId,
                                applicationId: app.applicationId,
                              },
                            })
                          }
                          variant="ghost"
                        >
                          {t("common.view")}
                        </IqujiButton>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </section>
      ))}
    </div>
  );
}

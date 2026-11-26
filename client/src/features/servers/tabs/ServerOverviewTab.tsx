import { IqujiBadge, IqujiButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";
import { workspacePages } from "@/contracts/pages";

import type { SecretAsset, Server, ServerRuntimeMetrics } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { StatCard } from "@/shared/ui/stat-card";
import { SurfacePanel } from "@/shared/ui/surface-panel";
import type { ServerHealth, ServerResources } from "../api";
import { runtimeCheckOutcome } from "../runtime-presentation";
import { ServerCredentialsSection } from "./ServerCredentialsSection";
import { ServerRuntimeFacts } from "./ServerRuntimeFacts";

type WorkloadApp = {
  id: string;
  name: string;
  projectId?: string;
  status?: string;
};

type ServerWorkloadRow = {
  id: string;
  name: string;
  projectId?: string;
  projectName: string;
  status?: string;
};

export function ServerOverviewTab({
  health,
  resources,
  secrets,
  server,
  onChanged,
}: {
  server: Server;
  health?: ServerHealth;
  resources?: ServerResources;
  secrets: SecretAsset[];
  onChanged: () => void;
}) {
  const navigate = useNavigate();
  const { t } = useI18n();
  const metrics = (health?.serverHealth?.metrics ?? {}) as ServerRuntimeMetrics;
  const checks = health?.process?.checks ?? [];
  const checkedAt = health?.serverHealth?.checkedAt ?? metrics.checkedAt;

  const projects = resources?.projects ?? [];
  const applications = (resources?.applications ?? []) as WorkloadApp[];

  // 构造简要微服务列表数据
  const workloadRows = useMemo<ServerWorkloadRow[]>(() => {
    const projectMap = new Map(projects.map((p) => [p.id, p.name]));
    return applications.map((app) => ({
      id: app.id,
      name: app.name,
      projectId: app.projectId,
      projectName: app.projectId
        ? (projectMap.get(app.projectId) ?? app.projectId)
        : t("servers.unlinkedProject"),
      status: app.status,
    }));
  }, [projects, applications, t]);

  return (
    <div className="ops-server-dashboard">
      <div className="ops-stat-grid">
        <StatCard
          helper={t("servers.statProjectsHelper")}
          icon={<IqujiIcon decorative name="folder" />}
          label={t("servers.statProjects")}
          testId="server-stat-projects"
          value={projects.length}
        />
        <StatCard
          helper={t("servers.statAppsHelper")}
          icon={<IqujiIcon decorative name="boxes" />}
          label={t("servers.statApps")}
          testId="server-stat-apps"
          value={applications.length}
        />
      </div>

      <div className="ops-server-dashboard__grid">
        {/* 左栏：平台规格与简要微服务应用 */}
        <div className="ops-server-dashboard__main">
          <ServerRuntimeFacts metrics={metrics} server={server} />

          <SurfacePanel
            actions={
              <IqujiButton
                compact
                onClick={() => void navigate({ to: workspacePages.projects.path })}
                variant="ghost"
              >
                {t("servers.goToProjects")}
                <IqujiIcon decorative name="arrow-right" size={13} />
              </IqujiButton>
            }
            title={t("servers.associatedApps", { count: String(workloadRows.length) })}
          >
            {workloadRows.length === 0 ? (
              <div className="ops-compact-empty">{t("servers.noApps", { name: server.name })}</div>
            ) : (
              <div className="ops-workload-compact-list">
                {workloadRows.map((item) => (
                  <div className="ops-workload-compact-item" key={item.id}>
                    <div className="ops-workload-compact-item__info">
                      <IqujiIcon decorative name="boxes" size={15} />
                      <span className="ops-workload-compact-item__name">{item.name}</span>
                      <span className="ops-workload-compact-item__project">
                        <IqujiIcon decorative name="folder" size={12} />
                        {item.projectName}
                      </span>
                    </div>
                    <div className="ops-row">
                      {(() => {
                        const isRunning =
                          item.status === "running" ||
                          item.status === "success" ||
                          item.status === "online" ||
                          item.status === "ready" ||
                          item.status === "succeeded";
                        const isFailed =
                          item.status === "failed" ||
                          item.status === "danger" ||
                          item.status === "error";
                        const tone = isRunning ? "success" : isFailed ? "danger" : "neutral";
                        const label = isRunning
                          ? t("state.running")
                          : isFailed
                            ? t("state.failed")
                            : item.status
                              ? t(`state.${item.status}` as any) || item.status
                              : t("state.not_deployed");
                        return <IqujiBadge tone={tone}>{label}</IqujiBadge>;
                      })()}
                      <IqujiButton
                        compact
                        onClick={() => void navigate({ to: workspacePages.projects.path })}
                        variant="ghost"
                      >
                        <IqujiIcon decorative name="external-link" size={13} />
                      </IqujiButton>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </SurfacePanel>
        </div>

        {/* 右栏：运维凭据与健康诊断流水 */}
        <div className="ops-server-dashboard__side">
          {/* 部署 SSH 凭据 */}
          <ServerCredentialsSection onChanged={onChanged} secrets={secrets} server={server} />

          {/* 系统健康诊断体检流水 */}
          <SurfacePanel
            description={
              checkedAt
                ? t("servers.probeTime", { time: new Date(checkedAt).toLocaleTimeString() })
                : undefined
            }
            title={t("servers.diagnosticsTitle")}
          >
            {checks.length === 0 ? (
              <div className="ops-compact-empty">{t("servers.noDiagnostics")}</div>
            ) : (
              <div className="ops-check-list">
                {checks.map((check, index) => {
                  const outcome = runtimeCheckOutcome(check);
                  const tone =
                    outcome === "passed"
                      ? "success"
                      : outcome === "warning"
                        ? "warning"
                        : outcome === "failed"
                          ? "danger"
                          : "neutral";
                  const labels: Record<string, string> = {
                    passed: t("common.opCompleted"),
                    warning: t("state.degraded"),
                    failed: t("state.failed"),
                    skipped: t("servers.notEnabled"),
                    unknown: t("common.unknown"),
                  };
                  return (
                    <div className="ops-check-item" key={String(check.id ?? check.code ?? index)}>
                      <IqujiBadge tone={tone}>{labels[outcome] ?? outcome}</IqujiBadge>
                      <span className="ops-check-item__code">{String(check.code ?? "CHECK")}</span>
                      <span className="ops-check-item__summary">
                        {String(check.summary ?? t("servers.diagnosticsNormal"))}
                      </span>
                    </div>
                  );
                })}
              </div>
            )}
          </SurfacePanel>
        </div>
      </div>
    </div>
  );
}

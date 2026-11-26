import { IqujiAlert, IqujiButton, IqujiInput } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { workspacePages } from "@/contracts/pages";
import type {
  MonitoringAgent,
  MonitoringProject,
  MonitoringServer,
} from "@/features/monitoring/api";
import {
  fetchMonitoringAgents,
  fetchMonitoringProjects,
  fetchMonitoringServers,
  fetchSwarmOverview,
  MONITORING_REFRESH_MS,
} from "@/features/monitoring/api";
import { MonitoringProjectsGroupView } from "@/features/monitoring/components/MonitoringProjectsGroupView";
import { MonitoringServersTable } from "@/features/monitoring/components/MonitoringServersTable";
import {
  MonitoringStatCards,
  type MonitoringTabKey,
} from "@/features/monitoring/components/MonitoringStatCards";
import { MonitoringSwarmView } from "@/features/monitoring/components/MonitoringSwarmView";
import { calculateSystemHealth } from "@/features/monitoring/monitoring-utils";
import { errorMessage } from "@/shared/api/client";
import { useAutomationScope } from "@/shared/desktop/automation-controls";
import { useI18n } from "@/shared/i18n";
import { AdminPageFrame } from "@/shared/ui/admin-page-frame";

export function MonitoringPage() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const search = useSearch({ strict: false });
  const { t, locale } = useI18n();

  const urlTab = (search as { tab?: string })?.tab;
  const initialTab: MonitoringTabKey =
    urlTab === "swarm" ? "swarm" : urlTab === "servers" ? "servers" : "projects";

  const [tab, setTab] = useState<MonitoringTabKey>(initialTab);
  const [searchKeyword, setSearchKeyword] = useState("");
  const [onlyAbnormal, setOnlyAbnormal] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);

  useEffect(() => {
    if (urlTab === "swarm" || urlTab === "servers" || urlTab === "projects") {
      setTab(urlTab as MonitoringTabKey);
    }
  }, [urlTab]);

  const handleSelectTab = (selectedTab: MonitoringTabKey) => {
    setTab(selectedTab);
    void navigate({ to: workspacePages.home.path, search: { tab: selectedTab } });
  };

  const refetchInterval = autoRefresh ? MONITORING_REFRESH_MS : false;

  const projects = useQuery({
    queryKey: ["monitoring-projects"],
    queryFn: fetchMonitoringProjects,
    refetchInterval,
  });

  const servers = useQuery({
    queryKey: ["monitoring-servers"],
    queryFn: fetchMonitoringServers,
    refetchInterval,
  });

  const swarm = useQuery({
    queryKey: ["monitoring-swarm"],
    queryFn: fetchSwarmOverview,
    refetchOnMount: "always",
    refetchInterval,
  });

  const agents = useQuery({
    queryKey: ["monitoring-agents"],
    queryFn: fetchMonitoringAgents,
    refetchInterval,
  });

  const activeQuery = tab === "swarm" ? swarm : tab === "servers" ? servers : projects;
  const lastRefreshedAt = activeQuery.dataUpdatedAt
    ? new Date(activeQuery.dataUpdatedAt).toLocaleTimeString(locale)
    : t("monitoring.notRefreshed");
  const isRefreshing =
    projects.isFetching || servers.isFetching || swarm.isFetching || agents.isFetching;

  const projectList: MonitoringProject[] = projects.data?.items ?? [];
  const serverList: MonitoringServer[] = servers.data?.items ?? [];
  const agentList: MonitoringAgent[] = agents.data?.items ?? [];

  // 计算去重后的受管项目数
  const uniqueProjectCount = useMemo(() => {
    return new Set(projectList.map((p) => p.projectId || p.projectName)).size;
  }, [projectList]);

  const summary = useMemo(
    () => calculateSystemHealth(projectList, serverList, swarm.data ?? {}, agentList, t),
    [projectList, serverList, swarm.data, agentList, t],
  );

  const handleManualRefresh = () =>
    Promise.all(
      ["monitoring-projects", "monitoring-servers", "monitoring-swarm", "monitoring-agents"].map(
        (key) => queryClient.invalidateQueries({ queryKey: [key] }),
      ),
    );
  const toggle = (value: string) => {
    if (value !== "true" && value !== "false") throw Error("AUTOMATION_FIELD_INVALID");
    return value === "true";
  };
  useAutomationScope("home", "", tab, undefined, {
    search: { value: searchKeyword, run: setSearchKeyword },
    onlyAbnormal: { value: String(onlyAbnormal), run: (v) => setOnlyAbnormal(toggle(v)) },
    autoRefresh: { value: String(autoRefresh), run: (v) => setAutoRefresh(toggle(v)) },
    refresh: { run: handleManualRefresh },
  });

  return (
    <AdminPageFrame
      actions={
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <IqujiButton
            variant="ghost"
            type="button"
            className="ops-refresh-pill-btn"
            data-ops-guide-control="home.autoRefresh"
            onClick={() => setAutoRefresh(!autoRefresh)}
            title={autoRefresh ? `${MONITORING_REFRESH_MS / 1000}s` : t("monitoring.paused")}
          >
            <span
              className={`ops-status-dot ${autoRefresh ? "ops-status-dot--pulse" : ""}`}
              data-tone={autoRefresh ? "success" : "neutral"}
            />
            <span className="ops-refresh-pill-label">
              {autoRefresh
                ? `${MONITORING_REFRESH_MS / 1000}s ${t("monitoring.autoRefresh")}`
                : t("monitoring.paused")}
            </span>
            <span className="ops-refresh-pill-time">{lastRefreshedAt}</span>
          </IqujiButton>

          <IqujiButton
            compact
            disabled={isRefreshing}
            iconStart={<IqujiIcon decorative name="refresh" size={13} />}
            loading={isRefreshing}
            data-ops-guide-control="home.refresh"
            onClick={handleManualRefresh}
            variant="secondary"
          >
            {t("monitoring.refresh")}
          </IqujiButton>
        </div>
      }
      description={t("monitoring.desc")}
      title={t("monitoring.title")}
    >
      {projects.isError || servers.isError || swarm.isError || agents.isError ? (
        <IqujiAlert title={t("monitoring.alertTitle")} tone="danger">
          {errorMessage(projects.error ?? servers.error ?? swarm.error ?? agents.error)}
        </IqujiAlert>
      ) : null}

      <MonitoringStatCards activeTab={tab} onSelectTab={handleSelectTab} summary={summary} />

      <div className="ops-workbench-box">
        <div className="ops-workbench-toolbar" data-ops-tutorial="workspace.home">
          <div className="ops-workbench-toolbar__left">
            <div className="ops-active-view-header">
              <span className="ops-active-view-title">
                {tab === "projects" && `${t("monitoring.tabProjects")} (${uniqueProjectCount})`}
                {tab === "servers" && `${t("monitoring.tabServers")} (${serverList.length})`}
                {tab === "swarm" &&
                  `${t("monitoring.tabSwarm")} (${summary.swarmClusterCount ?? "—"})`}
              </span>
              <span className="ops-active-view-desc">
                {tab === "projects" && t("monitoring.tabProjectsDesc")}
                {tab === "servers" && t("monitoring.tabServersDesc")}
              </span>
            </div>
          </div>

          <div className="ops-workbench-toolbar__right">
            {tab !== "swarm" ? (
              <div className="ops-filter-segmented">
                <IqujiButton
                  variant="ghost"
                  type="button"
                  className={`ops-filter-segmented__btn ${!onlyAbnormal ? "ops-filter-segmented__btn--active" : ""}`}
                  onClick={() => setOnlyAbnormal(false)}
                >
                  {t("monitoring.all")}
                </IqujiButton>
                <IqujiButton
                  variant="ghost"
                  type="button"
                  className={`ops-filter-segmented__btn ${onlyAbnormal ? "ops-filter-segmented__btn--active" : ""}`}
                  data-ops-guide-control="home.onlyAbnormal"
                  onClick={() => setOnlyAbnormal(true)}
                >
                  {t("monitoring.onlyAbnormal")}
                </IqujiButton>
              </div>
            ) : null}

            <div className="ops-search-input-wrap">
              <span className="ops-search-input-wrap__icon">
                <IqujiIcon decorative name="search" size={14} />
              </span>
              <IqujiInput
                placeholder={
                  tab === "swarm" ? t("swarm.searchPlaceholder") : t("monitoring.searchPlaceholder")
                }
                data-ops-guide-control="home.search"
                value={searchKeyword}
                onChange={(e) => setSearchKeyword(e.target.value)}
              />
            </div>
          </div>
        </div>

        <div className="ops-workbench-body">
          {tab === "projects" ? (
            <MonitoringProjectsGroupView
              isLoading={projects.isLoading}
              items={projectList}
              onlyAbnormal={onlyAbnormal}
              searchKeyword={searchKeyword}
            />
          ) : null}

          {tab === "servers" ? (
            <MonitoringServersTable
              isLoading={servers.isLoading}
              items={serverList}
              onlyAbnormal={onlyAbnormal}
              searchKeyword={searchKeyword}
            />
          ) : null}

          {tab === "swarm" ? (
            <MonitoringSwarmView
              isLoading={swarm.isLoading}
              searchKeyword={searchKeyword}
              serverList={serverList}
              swarm={swarm.data ?? {}}
            />
          ) : null}
        </div>
      </div>
    </AdminPageFrame>
  );
}

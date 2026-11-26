import {
  IqujiAlert,
  IqujiAsyncStateFrame,
  IqujiButton,
  IqujiEmptyState,
  IqujiLoadingState,
  IqujiLogViewer,
  IqujiPagination,
  IqujiSelect,
  IqujiTabs,
} from "@iquji-community/web-component";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useLayoutEffect, useState } from "react";
import type { ApplicationRuntime, SwarmServiceConfig } from "@/contracts/types";
import { errorMessage, operationErrorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { SectionPanel } from "@/shared/ui/section-panel";
import { fetchApplicationCommandLogs, fetchServiceLogs } from "./api";

import { bindLogGuide } from "./application-log-guide";

export function ApplicationLogsSection({
  applicationId,
  services,
  runtime,
  runtimeLoading = false,
  logCommand,
}: {
  applicationId: string;
  services: SwarmServiceConfig[];
  runtime?: ApplicationRuntime;
  runtimeLoading?: boolean;
  logCommand?: string[];
}) {
  const { locale, t } = useI18n();
  const [slot, setSlot] = useState(services[0]?.slotKey ?? "");
  const [source, setSource] = useState<"service" | "application">("service");
  const [selectedTask, setSelectedTask] = useState("");
  const service = runtime?.services.find((item) => item.serviceSlotKey === slot);
  const runningTasks = service?.tasks.filter((task) => task.status === "running") ?? [];
  const tasks = runningTasks.filter((task) => task.serverId && task.nodeId);
  const taskId = selectedTask
    ? (tasks.find((task) => task.id === selectedTask)?.id ?? "")
    : (tasks[0]?.id ?? "");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(50);

  useEffect(() => {
    if (!services.some((item) => item.slotKey === slot)) {
      setSlot(services[0]?.slotKey ?? "");
      setSelectedTask("");
    }
    setPage(1);
  }, [services, slot]);

  useLayoutEffect(
    () =>
      bindLogGuide(applicationId, (requestedSlot, requestedTask) => {
        if (!logCommand?.length) throw Error("APPLICATION_LOG_COMMAND_NOT_CONFIGURED");
        if (!services.some((item) => item.slotKey === requestedSlot))
          throw Error("AUTOMATION_OBJECT_UNAVAILABLE");
        if (runtime?.availability !== "available") throw Error("AUTOMATION_OBJECT_UNAVAILABLE");
        const available = runtime.services
          .find((item) => item.serviceSlotKey === requestedSlot)
          ?.tasks.filter((item) => item.status === "running" && item.serverId && item.nodeId);
        const replica = requestedTask
          ? available?.find((item) => item.id === requestedTask)
          : available?.[0];
        if (!replica) throw Error("AUTOMATION_OBJECT_UNAVAILABLE");
        setSlot(requestedSlot);
        setSelectedTask(replica.id);
        setSource("application");
        setPage(1);
      }),
    [applicationId, services, runtime, logCommand],
  );
  const containerLogs = useQuery({
    queryKey: ["application-service-logs", applicationId, slot],
    queryFn: () => fetchServiceLogs(applicationId, slot),
    enabled: source === "service" && Boolean(slot),
    refetchInterval: source === "service" ? 5000 : false,
  });

  const queryLogs = useQuery({
    queryKey: ["application-command-logs", applicationId, slot, taskId, logCommand],
    queryFn: ({ signal }) => fetchApplicationCommandLogs(applicationId, slot, taskId, signal),
    enabled: false,
    retry: false,
    gcTime: 0,
  });
  const logs = source === "service" ? containerLogs : queryLogs;
  const label = t(
    source === "service" ? "projects.swarmServiceLog" : "projects.applicationQueryLog",
  );
  const selectionHint = () => {
    if (!logCommand?.length) return t("projects.noLogQuery");
    if (runtime?.availability === "not_deployed") return t("projects.serviceNotDeployedTitle");
    if (runtime?.availability !== "available") {
      return runtime?.unavailableReason
        ? operationErrorMessage(runtime.unavailableReason)
        : t("projects.checkRemoteStatusPrompt");
    }
    if (!runningTasks.length) return t("projects.noLogReplica");
    return t("projects.changedLogReplica");
  };
  const structuredLogs = (logs.data?.items ?? []).map((item) => ({
    id: item.id,
    timestamp: item.timestamp,
    level: "info",
    source: item.source,
    phase: item.serviceSlotKey,
    message: item.message,
  }));

  const totalLogs = structuredLogs.length;
  const pageCount = Math.max(1, Math.ceil(totalLogs / pageSize));
  const safePage = Math.min(page, pageCount);
  const pagedLogs = structuredLogs.slice((safePage - 1) * pageSize, safePage * pageSize);

  const waitingRuntime =
    source === "application" && services.length > 0 && runtimeLoading && !runtime;
  const loadingInitialServiceLogs =
    source === "service" && Boolean(slot) && containerLogs.isPending && !containerLogs.error;
  const loadingInitialAppLogs = source === "application" && queryLogs.isFetching && !queryLogs.data;
  const isInitialLoading =
    services.length > 0 &&
    !logs.error &&
    (waitingRuntime || loadingInitialServiceLogs || loadingInitialAppLogs);
  const loadingLabel = waitingRuntime
    ? t("projects.checkingRemoteSwarm")
    : source === "application"
      ? t("projects.queryingApplicationLogs")
      : t("projects.readingLogs");

  return (
    <SectionPanel
      className="ops-application-logs-panel"
      icon="terminal"
      title={t("apps.tabLogs")}
      actions={
        services.length ? (
          <div className="ops-application-logs__actions">
            <IqujiSelect
              compact
              data-ops-guide-action="log_slot"
              aria-label={t("projects.logService")}
              onValueChange={(value) => {
                setSelectedTask("");
                setSlot(value ?? "");
              }}
              options={services.map((item) => ({
                label: item.serviceName || item.slotKey,
                value: item.slotKey,
              }))}
              value={slot}
            />
          </div>
        ) : null
      }
    >
      <div className="ops-application-logs">
        <IqujiTabs
          label={t("apps.tabLogs")}
          value={source}
          onChange={(next: "service" | "application") => {
            setSource(next);
            setPage(1);
          }}
          tabs={[
            { value: "service", label: t("projects.swarmServiceLog") },
            { value: "application", label: t("projects.applicationQueryLog") },
          ]}
        />
        {source === "application" && services.length && !waitingRuntime ? (
          <div className="ops-row">
            {Boolean(logCommand?.length) && tasks.length > 0 ? (
              <>
                <IqujiSelect
                  compact
                  aria-label={t("projects.logReplica")}
                  value={taskId}
                  options={tasks.map((task) => ({ label: task.id, value: task.id }))}
                  onValueChange={(value) => {
                    setSelectedTask(value ?? "");
                    setPage(1);
                  }}
                />
                {taskId ? (
                  <IqujiButton
                    data-ops-guide-action="application_logs"
                    data-ops-guide-slot={slot}
                    data-ops-guide-task={taskId}
                    loading={queryLogs.isFetching}
                    onClick={() => void queryLogs.refetch()}
                  >
                    {t("projects.queryApplicationLogs")}
                  </IqujiButton>
                ) : null}
              </>
            ) : null}
            {!logCommand?.length || !taskId ? (
              <IqujiAlert tone="info" title={selectionHint()} />
            ) : null}
          </div>
        ) : null}
        {logs.data?.truncated ? (
          <IqujiAlert tone="info" title={t("projects.logTruncated")} />
        ) : null}
        {!services.length ? (
          <IqujiEmptyState
            title={t("projects.emptyLogSource")}
            body={t("projects.emptyLogSourceDesc")}
          />
        ) : null}

        {isInitialLoading ? (
          <div className="ops-application-logs__loading">
            <IqujiLoadingState label={loadingLabel} />
          </div>
        ) : null}

        {logs.error ? (
          <IqujiAlert title={t("projects.readLogsFailed")} tone="danger">
            {errorMessage(logs.error)}
          </IqujiAlert>
        ) : null}

        {logs.data?.availability !== "available" && logs.data ? (
          <IqujiAlert
            title={
              logs.data.availability === "not_deployed"
                ? t("projects.serviceNotDeployedTitle")
                : t("projects.serviceLogsUnavailable")
            }
            tone="info"
          >
            {logs.data.unavailableReason
              ? operationErrorMessage(logs.data.unavailableReason)
              : t("projects.checkRemoteStatusPrompt")}
          </IqujiAlert>
        ) : null}

        {logs.data?.availability === "available" ? (
          <IqujiAsyncStateFrame
            state={source === "application" && queryLogs.isFetching ? "refreshing" : "ready"}
            refreshLabel={t("projects.queryingApplicationLogs")}
          >
            <div className="ops-application-logs__viewer-wrapper">
              <IqujiLogViewer
                emptyLabel={t("projects.emptyLogMsg")}
                fill
                label={label}
                locale={locale}
                loading={logs.isLoading}
                logs={pagedLogs}
                onRefresh={source === "service" ? () => void containerLogs.refetch() : undefined}
                refreshLabel={t("common.refresh")}
                refreshing={logs.isFetching}
                actions={
                  <div className="ops-application-logs__page-size">
                    <IqujiSelect
                      compact
                      aria-label={t("projects.pageSize")}
                      value={String(pageSize)}
                      options={[
                        { value: "20", label: t("projects.linesPerPage", { count: 20 }) },
                        { value: "50", label: t("projects.linesPerPage", { count: 50 }) },
                        { value: "100", label: t("projects.linesPerPage", { count: 100 }) },
                        { value: "200", label: t("projects.linesPerPage", { count: 200 }) },
                      ]}
                      onValueChange={(val) => {
                        if (val) {
                          setPageSize(Number(val));
                          setPage(1);
                        }
                      }}
                    />
                  </div>
                }
              />
              {totalLogs > 0 ? (
                <div className="ops-application-logs__pagination-bar">
                  <IqujiPagination
                    page={safePage}
                    pageSize={pageSize}
                    total={totalLogs}
                    label={label}
                    previousLabel={t("operations.previousPage")}
                    nextLabel={t("operations.nextPage")}
                    summary={(total, range) => (
                      <span className="ops-application-logs__summary">
                        {t("projects.logRangeSummary", {
                          from: range[0],
                          to: range[1],
                          total,
                        })}
                      </span>
                    )}
                    onPageChange={(nextPage) => setPage(nextPage)}
                  />
                </div>
              ) : null}
            </div>
          </IqujiAsyncStateFrame>
        ) : null}
      </div>
    </SectionPanel>
  );
}

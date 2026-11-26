import {
  type ColumnDef,
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiDataTable,
  IqujiDiagnosticBanner,
  IqujiEmptyState,
  IqujiLoadingState,
  IqujiTabs,
} from "@iquji-community/web-component";
import { useQuery } from "@tanstack/react-query";
import { useCallback, useMemo, useState } from "react";
import { errorMessage, operationErrorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { OperationLogViewerPanel } from "@/shared/ui/operation-log-viewer-panel";
import { fetchTunnelRuns } from "./api";
import type { FrpTunnelRun } from "./types";
import {
  formatDuration,
  formatFriendlyTime,
  formatRelativeTime,
  getTunnelStatusMeta,
} from "./utils";

export function TunnelHistoryTab({
  tunnelId,
  initialSelected = null,
}: {
  tunnelId: string;
  initialSelected?: FrpTunnelRun | null;
}) {
  const [selected, setSelected] = useState<FrpTunnelRun | null>(initialSelected);
  const [logKind, setLogKind] = useState<"start" | "stop">("start");
  const { t } = useI18n();
  const runs = useQuery({
    queryFn: () => fetchTunnelRuns(tunnelId, 1, 50),
    queryKey: ["tunnel-runs", tunnelId],
  });

  const openRun = useCallback((run: FrpTunnelRun) => {
    setSelected(run);
    setLogKind(run.startOperationRunId ? "start" : "stop");
  }, []);

  const items = runs.data?.items ?? [];
  const latestFailure = items[0]?.status === "failed" ? items[0] : undefined;

  const hasStartLog = Boolean(selected?.startOperationRunId);
  const hasStopLog = Boolean(selected?.stopOperationRunId);
  const effectiveLogKind: "start" | "stop" =
    logKind === "stop" && hasStopLog ? "stop" : hasStartLog ? "start" : "stop";
  const activeLogOperationId =
    effectiveLogKind === "stop" ? selected?.stopOperationRunId : selected?.startOperationRunId;

  const columns = useMemo<ColumnDef<FrpTunnelRun>[]>(
    () => [
      {
        accessorKey: "status",
        cell: ({ row }) => {
          const meta = getTunnelStatusMeta(row.original.status, t);
          return <IqujiBadge tone={meta.tone}>{meta.label}</IqujiBadge>;
        },
        header: t("tunnels.historyColStatus"),
      },
      {
        cell: ({ row }) => (
          <div>
            <div>{formatFriendlyTime(row.original.startedAt || row.original.createdAt)}</div>
            <span className="ops-muted">
              {formatRelativeTime(row.original.startedAt || row.original.createdAt, t)}
            </span>
          </div>
        ),
        header: t("tunnels.historyColStart"),
        id: "startedAt",
      },
      {
        cell: ({ row }) => (
          <span>
            {row.original.stoppedAt
              ? formatFriendlyTime(row.original.stoppedAt)
              : row.original.status === "running"
                ? t("tunnels.historyRunning")
                : "—"}
          </span>
        ),
        header: t("tunnels.historyColStop"),
        id: "stoppedAt",
      },
      {
        cell: ({ row }) => (
          <span>
            {formatDuration(
              row.original.startedAt || row.original.createdAt,
              row.original.stoppedAt,
              t,
            )}
          </span>
        ),
        header: t("tunnels.historyColDuration"),
        id: "duration",
      },
      {
        cell: ({ row }) => <span className="ops-muted">{row.original.containerName || "—"}</span>,
        header: t("tunnels.historyColContainer"),
        id: "container",
      },
      {
        cell: ({ row }) => (
          <IqujiButton
            compact
            onClick={(event) => {
              event.stopPropagation();
              openRun(row.original);
            }}
            variant="ghost"
          >
            {t("tunnels.probeDetails")}
            <OperationActivityDot
              scope={{ targetKind: "frp_tunnel_run", targetId: row.original.id }}
            />
          </IqujiButton>
        ),
        header: "",
        id: "actions",
      },
    ],
    [openRun, t],
  );

  return (
    <div className="ops-stack">
      {runs.isError ? (
        <IqujiAlert title={t("tunnels.historyLoadFailed")} tone="danger">
          {errorMessage(runs.error)}
        </IqujiAlert>
      ) : null}
      {latestFailure ? (
        <IqujiDiagnosticBanner
          description={
            latestFailure.errorSummary
              ? operationErrorMessage(latestFailure.errorSummary)
              : t("tunnels.historyExitDuringStart")
          }
          title={t("tunnels.historyLastRunFailed")}
          tone="danger"
        />
      ) : null}
      {runs.isLoading ? (
        <IqujiLoadingState label={t("tunnels.historyLoading")} />
      ) : (
        <IqujiDataTable
          columns={columns}
          data={items}
          emptyBody={t("tunnels.historyEmptyBody")}
          emptyTitle={t("tunnels.noHistory")}
          label={t("tunnels.historyTableLabel")}
          onRowClick={openRun}
          rowKey={(row) => row.id}
        />
      )}

      <InspectorDrawer
        bodyClassName="ops-tunnel-run-drawer__body"
        className="ops-log-drawer ops-tunnel-run-drawer"
        description={selected ? t("tunnels.historyRunDesc", { id: selected.id }) : undefined}
        footer={
          <IqujiButton onClick={() => setSelected(null)} variant="secondary">
            {t("common.close")}
          </IqujiButton>
        }
        onClose={() => setSelected(null)}
        open={selected !== null}
        size="xl"
        title={t("tunnels.historyRunTitle")}
      >
        {selected ? (
          <div className="ops-tunnel-run-drawer__layout">
            {selected.errorSummary ? (
              <IqujiDiagnosticBanner
                description={operationErrorMessage(selected.errorSummary)}
                title={t("tunnels.historyFailureReason")}
                tone="danger"
              />
            ) : null}

            <dl className="ops-tunnel-run-summary">
              <div className="ops-tunnel-run-summary__item">
                <dt>{t("tunnels.historyColStatus")}</dt>
                <dd>
                  <IqujiBadge tone={getTunnelStatusMeta(selected.status, t).tone}>
                    {getTunnelStatusMeta(selected.status, t).label}
                  </IqujiBadge>
                </dd>
              </div>
              <div className="ops-tunnel-run-summary__item">
                <dt>{t("tunnels.historyColDuration")}</dt>
                <dd>
                  {formatDuration(selected.startedAt || selected.createdAt, selected.stoppedAt, t)}
                </dd>
              </div>
              <div className="ops-tunnel-run-summary__item">
                <dt>{t("tunnels.historyColStart")}</dt>
                <dd>{formatFriendlyTime(selected.startedAt || selected.createdAt)}</dd>
              </div>
              <div className="ops-tunnel-run-summary__item">
                <dt>{t("tunnels.historyColStop")}</dt>
                <dd>{selected.stoppedAt ? formatFriendlyTime(selected.stoppedAt) : "—"}</dd>
              </div>
              <div className="ops-tunnel-run-summary__item">
                <dt>{t("tunnels.historyColContainer")}</dt>
                <dd>
                  <code>{selected.containerName || "—"}</code>
                </dd>
              </div>
              <div className="ops-tunnel-run-summary__item">
                <dt>{t("tunnels.historyRemoteDir")}</dt>
                <dd>
                  <code>{selected.remoteDir || "—"}</code>
                </dd>
              </div>
            </dl>

            <section className="ops-tunnel-run-logs">
              <div className="ops-tunnel-run-logs__head">
                {hasStartLog && hasStopLog ? (
                  <IqujiTabs
                    label={t("operations.startLogs")}
                    onChange={setLogKind}
                    tabs={[
                      {
                        label: (
                          <span className="ops-row">
                            {t("operations.startLogs")}
                            <OperationActivityDot operationId={selected.startOperationRunId} />
                          </span>
                        ),
                        value: "start",
                      },
                      {
                        label: (
                          <span className="ops-row">
                            {t("operations.stopLogs")}
                            <OperationActivityDot operationId={selected.stopOperationRunId} />
                          </span>
                        ),
                        value: "stop",
                      },
                    ]}
                    value={effectiveLogKind}
                    variant="segmented"
                  />
                ) : (
                  <h3 className="ops-tunnel-run-logs__title">
                    {effectiveLogKind === "stop"
                      ? t("operations.stopLogs")
                      : t("operations.startLogs")}
                    {activeLogOperationId ? (
                      <OperationActivityDot operationId={activeLogOperationId} />
                    ) : null}
                  </h3>
                )}
              </div>

              <div className="ops-tunnel-run-logs__viewer">
                {activeLogOperationId ? (
                  <OperationLogViewerPanel
                    active
                    label={
                      effectiveLogKind === "stop"
                        ? t("operations.stopLogs")
                        : t("operations.startLogs")
                    }
                    operationId={activeLogOperationId}
                    showMetaBar
                  />
                ) : (
                  <IqujiEmptyState
                    body={t("operations.emptyLogs")}
                    title={t("operations.startLogs")}
                  />
                )}
              </div>
            </section>
          </div>
        ) : null}
      </InspectorDrawer>
    </div>
  );
}

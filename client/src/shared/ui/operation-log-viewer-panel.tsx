import {
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiLogViewer,
} from "@iquji-community/web-component";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { OperationRun } from "@/contracts/types";
import { errorMessage, fetchJson, operationErrorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import {
  fetchOperationLogPage,
  isFinishedOperation,
  operationTone,
  orderedLogEntries,
} from "@/shared/operations/log-api";
import { useOperationLogRead } from "@/shared/operations/use-operation-log-read";
import { RecordSnippet } from "./record-snippet";
import { useOperationError } from "./use-operation-feedback";
import "./operation-history.css";

/** 统一管理单次操作日志的轮询、分页去重、已读确认与取消动作，供抽屉与内嵌视图共享。 */
export function useOperationLogState(operationId: string | undefined, active: boolean) {
  const { t } = useI18n();
  const client = useQueryClient();
  const [confirmCancel, setConfirmCancel] = useState(false);

  const run = useQuery({
    queryKey: ["operation-run", operationId],
    queryFn: () =>
      fetchJson<OperationRun>(`operations/runs/${encodeURIComponent(operationId ?? "")}`),
    enabled: active && Boolean(operationId),
    refetchInterval: (query) =>
      active && !isFinishedOperation(query.state.data?.status ?? "") ? 2000 : false,
  });

  const logs = useInfiniteQuery({
    queryKey: ["operation-logs", operationId, run.data?.updatedAt, run.data?.status],
    queryFn: ({ pageParam }) => fetchOperationLogPage(operationId ?? "", pageParam),
    initialPageParam: 1,
    getNextPageParam: (last) =>
      last.page * last.pageSize < last.total ? last.page + 1 : undefined,
    enabled: active && Boolean(operationId) && run.isSuccess,
    refetchInterval: active && !isFinishedOperation(run.data?.status ?? "") ? 2000 : false,
  });

  const cancellation = useMutation({
    mutationFn: () =>
      fetchJson<OperationRun>(`operations/runs/${encodeURIComponent(operationId ?? "")}/cancel`, {
        method: "POST",
        body: {},
      }),
    onSuccess: async () => {
      setConfirmCancel(false);
      await Promise.all([
        client.invalidateQueries({ queryKey: ["operation-run", operationId] }),
        client.invalidateQueries({ queryKey: ["operation-runs"] }),
        client.invalidateQueries({ queryKey: ["operation-logs", operationId] }),
        client.invalidateQueries({ queryKey: ["operation-activity"] }),
      ]);
    },
  });

  useOperationError(cancellation.error, t("operations.cancelFailed"));
  useOperationLogRead(run.data, active && logs.isSuccess);

  const entries = orderedLogEntries(logs.data?.pages);

  const fault = run.error || logs.error;
  const operationError =
    run.data?.status === "failed" ? run.data.errorCode || run.data.summary : undefined;
  const queue = run.data?.queue;
  const queueReason =
    queue?.state === "pending_confirmation"
      ? t("operations.waitingConfirmation")
      : queue?.blockedByOperationRunId
        ? t("operations.waitingPredecessors", { count: Math.max(0, queue.position - 1) })
        : queue?.state === "queued"
          ? t("operations.waitingSlot")
          : undefined;
  const canCancel = Boolean(run.data && !isFinishedOperation(run.data.status) && queue?.canCancel);

  return {
    canCancel,
    cancellation,
    confirmCancel,
    entries,
    fault,
    logs,
    operationError,
    queue,
    queueReason,
    run,
    setConfirmCancel,
  };
}

export function OperationLogViewerPanel({
  operationId,
  active,
  label,
  showMetaBar = false,
  state: externalState,
}: {
  operationId?: string;
  active: boolean;
  label?: string;
  showMetaBar?: boolean;
  state?: ReturnType<typeof useOperationLogState>;
}) {
  const { t } = useI18n();
  const internalState = useOperationLogState(
    externalState ? undefined : operationId,
    externalState ? false : active,
  );
  const state = externalState ?? internalState;
  const {
    canCancel,
    cancellation,
    confirmCancel,
    entries,
    fault,
    logs,
    operationError,
    queue,
    queueReason,
    run,
    setConfirmCancel,
  } = state;

  return (
    <>
      <div className="ops-log-drawer__fill">
        {showMetaBar && operationId ? (
          <div className="ops-log-inline-meta">
            {run.data ? (
              <IqujiBadge tone={operationTone(run.data.status)}>
                {t(`operations.status.${run.data.status}`)}
              </IqujiBadge>
            ) : null}
            <RecordSnippet label={t("operations.operationId")} value={operationId} />
            {queue?.position ? (
              <span className="ops-log-drawer__queue-chip">
                {t("operations.queuePosition", { position: queue.position })}
              </span>
            ) : null}
            {queueReason ? <span className="ops-log-drawer__queue-chip">{queueReason}</span> : null}
            {canCancel ? (
              <IqujiButton compact onClick={() => setConfirmCancel(true)} variant="secondary">
                {t("operations.cancel")}
              </IqujiButton>
            ) : null}
          </div>
        ) : null}

        <IqujiLogViewer
          actions={
            logs.hasNextPage ? (
              <IqujiButton
                compact
                loading={logs.isFetchingNextPage}
                onClick={() => void logs.fetchNextPage()}
                variant="ghost"
              >
                {t("operations.loadMore")}
              </IqujiButton>
            ) : undefined
          }
          emptyLabel={t(
            run.data?.status === "queued" ? "operations.waitingLogs" : "operations.emptyLogs",
          )}
          error={
            fault
              ? { title: t("operations.logReadFailed"), message: errorMessage(fault) }
              : operationError
                ? {
                    title: t("common.opFailed"),
                    message: operationErrorMessage(operationError),
                    code: run.data?.errorCode,
                  }
                : undefined
          }
          fill
          label={label ?? t("operations.logs")}
          loading={run.isLoading || logs.isLoading}
          logs={entries.map((entry) => ({
            ...entry,
            message: operationErrorMessage(entry.message),
          }))}
          onRefresh={() => {
            void logs.refetch();
            void run.refetch();
          }}
          refreshing={logs.isFetching || run.isFetching}
          refreshLabel={t("common.refresh")}
        />
      </div>

      <IqujiConfirmDialog
        cancelLabel={t("common.back")}
        closeLabel={t("common.close")}
        confirmLabel={t("operations.cancel")}
        confirmLoading={cancellation.isPending}
        description={t("operations.cancelDescription")}
        onConfirm={() => cancellation.mutate()}
        onOpenChange={setConfirmCancel}
        open={confirmCancel}
        title={t("operations.cancelTitle")}
      />
    </>
  );
}

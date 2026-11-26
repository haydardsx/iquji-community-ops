import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiEmptyState,
  IqujiLoadingState,
  IqujiPagination,
  IqujiSelect,
} from "@iquji-community/web-component";
import { IqujiIcon, type IqujiIconName } from "@iquji-community/web-icons";
import { useQuery } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import type { OperationRun } from "@/contracts/types";
import { errorMessage, operationErrorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import {
  fetchOperationHistory,
  type OperationScope,
  operationTone,
} from "@/shared/operations/log-api";
import { InspectorDrawer } from "./inspector-drawer";
import { useOpenOperationLog } from "./operation-log-drawer";
import { RecordSnippet } from "./record-snippet";
import "./operation-history.css";

function operationStatusIcon(status: string): IqujiIconName {
  if (status === "succeeded") return "check-circle";
  if (status === "failed") return "circle-alert";
  if (status === "running") return "loader";
  if (status === "cancelled") return "minus";
  return "clock";
}

function technicalErrorCode(value?: string) {
  const code = value?.trim().split(":", 1)[0];
  return code && /^[A-Z][A-Z0-9_]+$/.test(code) ? code : undefined;
}

export function OperationHistoryButton({
  scope,
  label,
  renderActions,
}: {
  scope: OperationScope;
  label?: string;
  renderActions?: (run: OperationRun, rows: OperationRun[], index: number) => ReactNode;
}) {
  const { t, locale } = useI18n();
  const [open, setOpen] = useState(false);
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const openLog = useOpenOperationLog();
  const title = label ?? t("operations.logs");

  const history = useQuery({
    queryKey: ["operation-runs", "history", scope, page, status],
    queryFn: () => fetchOperationHistory(scope, page, status),
    enabled: open,
    refetchInterval: open ? 3000 : false,
  });

  const rows = history.data?.items ?? [];
  const pageSize = history.data?.pageSize ?? 20;
  const total = history.data?.total ?? 0;

  return (
    <>
      <IqujiButton
        compact
        variant="secondary"
        iconStart={<IqujiIcon decorative name="clock" />}
        onClick={() => setOpen(true)}
      >
        {title}
        <OperationActivityDot scope={scope} />
      </IqujiButton>
      <InspectorDrawer
        actionBarLayout="full"
        bodyClassName="ops-log-drawer__body--list"
        className="ops-log-drawer"
        open={open}
        onClose={() => {
          setOpen(false);
        }}
        title={title}
        description={t("operations.historyDescription")}
        size="lg"
        headerToolbar={
          <div className="ops-log-drawer__toolbar">
            <div className="ops-log-drawer__toolbar-lead">
              <span className="ops-log-drawer__count">
                {total === 0
                  ? t("operations.paginationEmpty")
                  : t("operations.page", { page, total })}
              </span>
            </div>
            <div className="ops-log-drawer__toolbar-controls">
              <div className="ops-log-drawer__filter">
                <IqujiSelect
                  aria-label={t("operations.filterStatus")}
                  value={status}
                  onValueChange={(value) => {
                    setStatus(value ?? "");
                    setPage(1);
                  }}
                  options={[
                    { value: "", label: t("operations.allStatuses") },
                    ...["queued", "running", "succeeded", "failed", "cancelled"].map((value) => ({
                      value,
                      label: t(`operations.status.${value}`),
                    })),
                  ]}
                />
              </div>
              <IqujiButton
                compact
                variant="secondary"
                iconStart={<IqujiIcon decorative name="refresh" size={13} />}
                loading={history.isFetching}
                onClick={() => void history.refetch()}
              >
                {t("common.refresh")}
              </IqujiButton>
            </div>
          </div>
        }
        footer={
          <IqujiPagination
            label={t("operations.paginationLabel")}
            previousLabel={t("operations.previousPage")}
            nextLabel={t("operations.nextPage")}
            page={page}
            pageSize={pageSize}
            total={total}
            summary={(total, [start, end]) =>
              total === 0
                ? t("operations.paginationEmpty")
                : t("operations.paginationSummary", { start, end, total })
            }
            onPageChange={setPage}
          />
        }
      >
        <div className="ops-log-drawer__state">
          {history.isLoading ? <IqujiLoadingState label={t("common.loading")} /> : null}
          {history.isError ? (
            <IqujiAlert tone="danger" title={t("operations.historyFailed")}>
              {errorMessage(history.error)}
            </IqujiAlert>
          ) : null}
          {history.isSuccess && !rows.length ? (
            <IqujiEmptyState
              title={t("operations.emptyHistory")}
              body={t("operations.emptyHistoryDescription")}
            />
          ) : null}
          {rows.length > 0 ? (
            <div className="ops-log-stream">
              {rows.map((run, index) => {
                const rawSummary = run.errorCode || run.summary;
                const code =
                  run.status === "failed" ? technicalErrorCode(run.errorCode) : undefined;
                return (
                  <article className="ops-log-card" data-status={run.status} key={run.id}>
                    <div className="ops-log-card__head">
                      <div className="ops-log-card__icon" data-status={run.status}>
                        <IqujiIcon decorative name={operationStatusIcon(run.status)} size={16} />
                      </div>
                      <div className="ops-log-card__heading">
                        <div className="ops-log-card__title-row">
                          <strong className="ops-log-card__title">
                            {operationActionLabel(run.action, t)}
                          </strong>
                          <IqujiBadge tone={operationTone(run.status)}>
                            {t(`operations.status.${run.status}`)}
                          </IqujiBadge>
                        </div>
                        <div className="ops-log-card__meta-row">
                          <span className="ops-log-card__target">
                            {run.targetName || run.targetId}
                          </span>
                          <span className="ops-log-card__dot" aria-hidden="true">
                            ·
                          </span>
                          <span className="ops-log-card__time">
                            <IqujiIcon decorative name="clock" size={12} />
                            <span>{new Date(run.createdAt).toLocaleString(locale)}</span>
                          </span>
                        </div>
                      </div>
                    </div>

                    {run.queue?.state === "queued" ? (
                      <p className="ops-log-card__queue">
                        <IqujiIcon decorative name="clock" size={13} />
                        <span>
                          {t("operations.queuePosition", { position: run.queue.position })} ·{" "}
                          {run.queue.blockedByOperationRunId
                            ? t("operations.waitingPredecessors", {
                                count: Math.max(0, run.queue.position - 1),
                              })
                            : t("operations.waitingSlot")}
                        </span>
                      </p>
                    ) : null}

                    {rawSummary ? (
                      <div className="ops-log-card__callout" data-status={run.status}>
                        <p className="ops-log-card__summary">{operationErrorMessage(rawSummary)}</p>
                        {code ? (
                          <div className="ops-log-card__code-row">
                            <code className="ops-log-card__code">{code}</code>
                          </div>
                        ) : null}
                      </div>
                    ) : null}

                    <div className="ops-log-card__footer">
                      <RecordSnippet value={run.id} label={t("operations.operationId")} />
                      <div className="ops-log-card__actions">
                        {renderActions?.(run, rows, index)}
                        <IqujiButton
                          compact
                          variant="secondary"
                          iconStart={<IqujiIcon decorative name="terminal" size={13} />}
                          onClick={() => openLog(run.id)}
                        >
                          {t("operations.viewLogs")}
                          <OperationActivityDot operationId={run.id} />
                        </IqujiButton>
                      </div>
                    </div>
                  </article>
                );
              })}
            </div>
          ) : null}
        </div>
      </InspectorDrawer>
    </>
  );
}

export function operationActionLabel(action: string, t: (key: string) => string) {
  const key = `operations.action.${action}`;
  const label = t(key);
  return label === key ? action : label;
}

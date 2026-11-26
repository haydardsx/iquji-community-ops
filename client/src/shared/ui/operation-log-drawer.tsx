import { IqujiBadge, IqujiButton } from "@iquji-community/web-component";
import { useNavigate, useRouter, useSearch } from "@tanstack/react-router";
import { useI18n } from "@/shared/i18n";
import { operationTone } from "@/shared/operations/log-api";
import { InspectorDrawer } from "./inspector-drawer";
import { operationActionLabel } from "./operation-history";
import { OperationLogViewerPanel, useOperationLogState } from "./operation-log-viewer-panel";
import { RecordSnippet } from "./record-snippet";
import "./operation-history.css";

export function useOpenOperationLog() {
  const router = useRouter({ warn: false });
  return (operationId: string) => {
    if (!router) throw new Error("日志定位需要 RouterProvider。");
    void router.navigate({
      to: ".",
      search: (current) => ({ ...current, operationId }),
      hash: true,
    });
  };
}

/** URL 中的操作 ID 让通知、红点引导与页面入口共享同一个日志定位。 */
export function OperationLogHost() {
  const search = useSearch({ strict: false }) as { operationId?: string };
  const navigate = useNavigate();
  return search.operationId ? (
    <OperationLogDrawer
      operationId={search.operationId}
      open
      onClose={() =>
        void navigate({
          to: ".",
          search: (current) => ({ ...current, operationId: undefined }),
          replace: true,
          hash: true,
        })
      }
    />
  ) : null;
}

export function OperationLogDrawer({
  operationId,
  open,
  onClose,
}: {
  operationId?: string;
  open: boolean;
  onClose: () => void;
}) {
  const { t, locale } = useI18n();
  const state = useOperationLogState(operationId, open);
  const { canCancel, queue, queueReason, run, setConfirmCancel } = state;

  return (
    <InspectorDrawer
      bodyClassName="ops-log-drawer__body--fill"
      className="ops-log-drawer"
      description={
        run.data
          ? `${run.data.targetName || run.data.targetId} · ${new Date(run.data.createdAt).toLocaleString(locale)}`
          : undefined
      }
      headerMeta={
        <div className="ops-log-drawer__meta">
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
      }
      onClose={onClose}
      open={open}
      size="xl"
      title={run.data ? operationActionLabel(run.data.action, t) : t("operations.logs")}
    >
      <OperationLogViewerPanel active={open} operationId={operationId} state={state} />
    </InspectorDrawer>
  );
}

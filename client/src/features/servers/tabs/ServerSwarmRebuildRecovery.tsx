import { IqujiAlert, IqujiButton, IqujiConfirmDialog } from "@iquji-community/web-component";
import { useQueryClient } from "@tanstack/react-query";
import type { OperationRun } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { recoverSwarmRebuild } from "../api";
import { swarmRebuildRecoveryOperationId } from "../swarm-rebuild-recovery";

export function ServerSwarmRebuildRecovery({
  run,
  onClose,
}: {
  run: OperationRun;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const operation = useConfirmedOperation();
  const queryClient = useQueryClient();
  const originalOperationId = swarmRebuildRecoveryOperationId(run);
  const locked = operation.isPending || Boolean(operation.operationId);
  const submit = async () => {
    if (!originalOperationId || operation.isPending) return;
    try {
      await operation.submit({
        action: "recover_docker_swarm_rebuild",
        targetKind: "server",
        targetId: run.targetId,
        label: t("servers.actionRecoverSwarmRebuild"),
        create: (requestKey) => recoverSwarmRebuild(run.targetId, originalOperationId, requestKey),
      });
      onClose();
      void queryClient.invalidateQueries({ queryKey: ["operation-runs"] });
    } catch {
      /* 保留待确认操作，通过统一反馈显示恢复失败原因。 */
    }
  };
  if (!originalOperationId) return null;
  return (
    <IqujiConfirmDialog
      open
      title={t("servers.recoverSwarmTitle")}
      description={run.targetName || t("servers.recoverSwarmDesc")}
      size="md"
      closeLabel={t("servers.closeRecoveryConfirm")}
      mandatory={Boolean(operation.operationId)}
      onOpenChange={(next) => {
        if (!next && !locked) onClose();
      }}
      cancelLabel={t("common.cancel")}
      confirmLabel={
        operation.operationId ? t("servers.retryConfirm") : t("servers.confirmContinueRecovery")
      }
      confirmLoading={operation.isPending}
      onConfirm={() => void submit()}
    >
      <div className="ops-form-stack">
        <IqujiAlert title={t("servers.recoveryNoticeTitle")} tone="warning">
          {t("servers.recoveryNoticeDesc")}
        </IqujiAlert>
        <p className="ops-muted">{t("servers.recoveryScopeDesc")}</p>
        {operation.operationId ? (
          <IqujiButton
            variant="secondary"
            disabled={operation.isPending}
            onClick={() =>
              void operation
                .cancelPending()
                .then(onClose)
                .catch(() => {})
            }
          >
            {t("servers.cancelOriginalOp")}
          </IqujiButton>
        ) : null}
      </div>
    </IqujiConfirmDialog>
  );
}

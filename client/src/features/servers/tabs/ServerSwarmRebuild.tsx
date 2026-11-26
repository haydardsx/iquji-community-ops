import { IqujiAlert, IqujiButton, IqujiConfirmDialog } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { Server, ServerRuntimeMetrics } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { rebuildSwarm } from "../api";
import { getSwarmConnectionFacts, type SwarmConnectionMode } from "../swarm-connection";
import { SwarmAddressFields, swarmAddressError } from "./SwarmAddressFields";

export function ServerSwarmRebuild({
  server,
  disabled,
  label: labelProp,
  initialConnectionMode,
  onChanged,
}: {
  server: Server;
  disabled?: boolean;
  label?: string;
  initialConnectionMode?: SwarmConnectionMode;
  onChanged: () => void;
}) {
  const { t } = useI18n();
  const label = labelProp ?? t("servers.actionRebuildSwarm");
  const [open, setOpen] = useState(false);
  const facts = getSwarmConnectionFacts((server.health?.metrics ?? {}) as ServerRuntimeMetrics, t);
  const defaultMode = initialConnectionMode ?? (facts.mode === "private" ? "private" : "public");
  const defaultAddress =
    facts.mode === defaultMode
      ? facts.address
      : (defaultMode === "public" ? server.publicHost : server.privateHost) || "";
  const [connectionMode, setConnectionMode] = useState<SwarmConnectionMode>(defaultMode);
  const [advertiseAddr, setAdvertiseAddr] = useState(defaultAddress);
  const operation = useConfirmedOperation();
  const queryClient = useQueryClient();
  const locked = operation.isPending || Boolean(operation.operationId);
  const canClose = !locked;
  const submit = async () => {
    if (operation.isPending || swarmAddressError(connectionMode, advertiseAddr, t)) return;
    try {
      await operation.submit({
        action: "rebuild_docker_swarm",
        targetKind: "server",
        targetId: server.id,
        label: t("servers.actionRebuildSwarm"),
        create: (requestKey) =>
          rebuildSwarm(
            server.id,
            {
              connectionMode,
              advertiseAddr: advertiseAddr.trim(),
            },
            requestKey,
          ),
      });
      setOpen(false);
      void queryClient.invalidateQueries({ queryKey: ["servers"] });
      onChanged();
    } catch {
      /* 确认失败保留原操作，统一反馈提供可重试原因。 */
    }
  };
  return (
    <>
      <IqujiButton
        compact
        variant="secondary"
        iconStart={<IqujiIcon decorative name="refresh" />}
        disabled={disabled || locked}
        onClick={() => {
          operation.reset();
          setConnectionMode(defaultMode);
          setAdvertiseAddr(defaultAddress);
          setOpen(true);
        }}
      >
        {label}
      </IqujiButton>
      <IqujiConfirmDialog
        open={open}
        size="md"
        onOpenChange={(next) => {
          if (!next && canClose) setOpen(false);
        }}
        title={t("servers.actionRebuildSwarm")}
        description={server.name}
        closeLabel={t("servers.closeRebuildConfirm")}
        cancelLabel={t("common.cancel")}
        mandatory={Boolean(operation.operationId)}
        confirmLabel={
          operation.operationId ? t("servers.retryConfirm") : t("servers.confirmRebuild")
        }
        confirmLoading={operation.isPending}
        confirmDisabled={Boolean(swarmAddressError(connectionMode, advertiseAddr, t))}
        onConfirm={() => void submit()}
      >
        <div className="ops-form-stack">
          <p className="ops-muted">
            {t("servers.currentActualConnection", { label: facts.label })}
          </p>
          <IqujiAlert title={t("servers.rebuildNoticeTitle")} tone="warning">
            {t("servers.rebuildNoticeDesc")}
          </IqujiAlert>
          <p className="ops-muted">{t("servers.rebuildScopeDesc")}</p>
          <SwarmAddressFields
            server={server}
            connectionMode={connectionMode}
            advertiseAddr={advertiseAddr}
            disabled={locked}
            onConnectionModeChange={setConnectionMode}
            onAdvertiseAddrChange={setAdvertiseAddr}
          />
          {operation.operationId ? (
            <IqujiButton
              variant="secondary"
              disabled={operation.isPending}
              onClick={() =>
                void operation
                  .cancelPending()
                  .then(() => setOpen(false))
                  .catch(() => {})
              }
            >
              {t("servers.cancelOriginalOp")}
            </IqujiButton>
          ) : null}
        </div>
      </IqujiConfirmDialog>
    </>
  );
}

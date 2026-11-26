import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiField,
  IqujiTextarea,
} from "@iquji-community/web-component";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { SecretAsset } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { useOperationNotice } from "@/shared/ui/use-operation-feedback";
import { rotateSecretAsset, SECRET_ASSETS_QUERY_KEY } from "./api";
import { SecretCopySnippet } from "./components/SecretCopySnippet";
import { assetTypeLabel, assetTypeTone, materialLabel, materialTone } from "./secrets-model";

export interface SecretRotationDrawerProps {
  open: boolean;
  asset: SecretAsset | null;
  onOpenChange: (open: boolean) => void;
  onRotated?: () => void;
}

export function SecretRotationDrawer({
  open,
  asset,
  onOpenChange,
  onRotated,
}: SecretRotationDrawerProps) {
  const { t, locale } = useI18n();
  const queryClient = useQueryClient();
  const notify = useOperationNotice();
  const operation = useConfirmedOperation();
  const [secretValue, setSecretValue] = useState("");

  const assetId = asset?.id;
  useEffect(() => {
    if (!open || !assetId) return;
    setSecretValue("");
    return () => setSecretValue("");
  }, [open, assetId]);

  if (!asset) return null;

  const rotate = async () => {
    if (asset.materialStatus === "pending") return;
    try {
      await operation.submit({
        action: "rotate_secret_asset",
        targetId: asset.id,
        targetKind: "secret_asset",
        label: t("secrets.rotateLabelAction", { name: asset.name }),
        create: async (requestKey) => {
          const result = await rotateSecretAsset(
            asset.id,
            asset.assetType === "application_secret" || asset.assetType === "registry_secret"
              ? secretValue
              : secretValue.trim(),
            requestKey,
          );
          if (!result.operation) throw new Error(t("secrets.rotateNoOpReturned"));
          return result.operation;
        },
      });
      setSecretValue("");
      notify(t("secrets.rotateSubmitted"));
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: SECRET_ASSETS_QUERY_KEY }),
        queryClient.invalidateQueries({ queryKey: ["secret-delete-preview", asset.id] }),
      ]);
      onRotated?.();
      onOpenChange(false);
    } catch {
      // 统一由全局操作反馈呈现
    }
  };

  const close = async () => {
    if (operation.isPending) return;
    if (operation.operationId) await operation.cancelPending();
    onOpenChange(false);
  };

  return (
    <InspectorDrawer
      description={t("secrets.rotateDrawerDesc")}
      footer={
        <>
          <IqujiButton
            disabled={operation.isPending}
            onClick={() => void close().catch(() => undefined)}
            variant="secondary"
          >
            {t("common.cancel")}
          </IqujiButton>
          <IqujiButton
            disabled={
              !secretValue.trim() || operation.isPending || asset.materialStatus === "pending"
            }
            loading={operation.isPending}
            onClick={() => void rotate()}
            variant="primary"
          >
            {t("secrets.confirmRotateAction")}
          </IqujiButton>
        </>
      }
      headerMeta={
        <div className="ops-drawer-header-badges">
          <IqujiBadge tone={assetTypeTone(asset.assetType)}>
            {assetTypeLabel(asset.assetType, t)}
          </IqujiBadge>
          <IqujiBadge tone={materialTone(asset.materialStatus)}>
            {materialLabel(asset.materialStatus, t)}
          </IqujiBadge>
        </div>
      }
      onClose={() => {
        void close().catch(() => undefined);
      }}
      open={open}
      size="md"
      title={t("secrets.rotateDrawerTitle", { name: asset.name })}
    >
      <div className="ops-form-stack">
        <div className="ops-rotation-state-card">
          <div className="ops-rotation-state-row">
            <span className="ops-rotation-state-label">{t("secrets.currentFingerprint")}</span>
            <SecretCopySnippet
              label={t("secrets.currentFingerprint")}
              value={asset.fingerprint ?? "—"}
            />
          </div>
          <div className="ops-rotation-state-row">
            <span className="ops-rotation-state-label">{t("secrets.lastRotatedTime")}</span>
            <span className="ops-rotation-state-value">
              {asset.rotatedAt
                ? new Date(asset.rotatedAt).toLocaleString(locale)
                : t("secrets.neverRotated")}
            </span>
          </div>
        </div>

        <IqujiField
          label={
            asset.assetType === "ssh_key"
              ? t("secrets.newSshKeyLabel")
              : asset.assetType === "application_secret" || asset.assetType === "registry_secret"
                ? t(
                    asset.assetType === "registry_secret"
                      ? "secrets.typeRegistrySecret"
                      : "secrets.typeApplicationSecret",
                  )
                : t("secrets.newGitTokenLabel")
          }
          required
        >
          <IqujiTextarea
            autoComplete="off"
            className="ops-font-mono"
            disabled={operation.isPending}
            onChange={(event) => setSecretValue(event.currentTarget.value)}
            placeholder={
              asset.assetType === "ssh_key"
                ? t("secrets.privateKeyPlaceholder")
                : asset.assetType === "application_secret" || asset.assetType === "registry_secret"
                  ? ""
                  : t("secrets.newGitTokenPlaceholder")
            }
            rows={7}
            spellCheck={false}
            value={secretValue}
          />
        </IqujiField>

        {operation.operationId ? (
          <IqujiAlert title={t("secrets.lastRotatePendingTitle")} tone="warning">
            {t("secrets.lastRotatePendingDesc")}
            <IqujiButton
              compact
              disabled={operation.isPending}
              onClick={() => void operation.cancelPending().catch(() => undefined)}
              variant="secondary"
            >
              {t("secrets.cancelOldTask")}
            </IqujiButton>
          </IqujiAlert>
        ) : null}
      </div>
    </InspectorDrawer>
  );
}

import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiDrawer,
  IqujiField,
  IqujiInput,
  IqujiMultiSelect,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { SecretAsset, Server } from "@/contracts/types";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { useOperationError, useOperationNotice } from "@/shared/ui/use-operation-feedback";
import {
  bindSecretToServer,
  fetchSecretDeletePreview,
  SECRET_ASSETS_QUERY_KEY,
  updateSecretAsset,
} from "./api";
import { SecretCopySnippet } from "./components/SecretCopySnippet";
import {
  assetTypeLabel,
  assetTypeTone,
  materialLabel,
  materialTone,
  purposeLabel,
} from "./secrets-model";

export interface SecretDetailDrawerProps {
  asset: SecretAsset | null;
  assets?: SecretAsset[];
  servers: Server[];
  onChanged: () => void;
  onOpenChange: (open: boolean) => void;
  onRequestDelete?: (asset: SecretAsset) => void;
  onOpenRotate?: (asset: SecretAsset) => void;
}

export function SecretDetailDrawer({
  asset,
  servers,
  onChanged,
  onOpenChange,
}: SecretDetailDrawerProps) {
  const { t, locale } = useI18n();
  const queryClient = useQueryClient();
  const notify = useOperationNotice();
  const notification = useIqujiNotification();

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [boundIds, setBoundIds] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);

  const boundSignature = asset
    ? servers
        .filter((server) => server.secretAssetIds?.includes(asset.id))
        .map((server) => server.id)
        .join(",")
    : "";

  useEffect(() => {
    if (!asset) return;
    setName(asset.name);
    setDescription(asset.description ?? "");
    setBoundIds(boundSignature ? boundSignature.split(",") : []);
  }, [asset, boundSignature]);

  const preview = useQuery({
    queryFn: () => fetchSecretDeletePreview(asset?.id ?? ""),
    queryKey: ["secret-delete-preview", asset?.id],
    enabled: Boolean(asset?.id),
  });
  useOperationError(preview.error, t("secrets.readImpactFailed"));

  if (!asset) return null;

  const materialReady = asset.materialStatus === "available";
  const originallyBound = new Set(
    servers
      .filter((server) => server.secretAssetIds?.includes(asset.id))
      .map((server) => server.id),
  );
  const hasForbiddenBinding = !materialReady && boundIds.some((id) => !originallyBound.has(id));
  const metaDirty = name.trim() !== asset.name || description.trim() !== (asset.description ?? "");
  const bindDirty = [...boundIds].sort().join(",") !== boundSignature;
  const isDirty = metaDirty || bindDirty;

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: SECRET_ASSETS_QUERY_KEY }),
      queryClient.invalidateQueries({ queryKey: ["servers"] }),
      queryClient.invalidateQueries({ queryKey: ["secret-delete-preview", asset.id] }),
    ]);
    onChanged();
  };

  const saveAll = async () => {
    setSaving(true);
    try {
      if (hasForbiddenBinding) throw new Error(t("operations.bindingMaterialNotReady"));
      if (metaDirty) {
        await updateSecretAsset(asset.id, {
          name: name.trim(),
          description: description.trim(),
        });
      }
      if (bindDirty && asset.assetType === "ssh_key") {
        await Promise.all(
          servers.map((server) => {
            const current = server.secretAssetIds ?? [];
            const shouldBind = boundIds.includes(server.id);
            if (shouldBind === current.includes(asset.id)) return Promise.resolve(null);
            const next = shouldBind
              ? [...new Set([...current, asset.id])]
              : current.filter((id) => id !== asset.id);
            return bindSecretToServer({ serverId: server.id, secretAssetIds: next });
          }),
        );
      }
      notify(t("secrets.savedSuccess"));
      await refresh();
      onOpenChange(false);
    } catch (error) {
      notification.open({
        title: t("secrets.saveFailed"),
        description: errorMessage(error),
        tone: "danger",
        duration: 0,
      });
    } finally {
      setSaving(false);
    }
  };

  const registry = asset.assetType === "registry_secret";
  const apps =
    (registry ? preview.data?.usage.registryApplications : preview.data?.usage.gitApplications) ??
    [];

  return (
    <IqujiDrawer
      actions={
        <div className="ops-drawer-footer-actions ops-drawer-footer-actions--end">
          <IqujiButton disabled={saving} onClick={() => onOpenChange(false)} variant="secondary">
            {t("common.cancel")}
          </IqujiButton>

          <IqujiButton
            disabled={!isDirty || !name.trim() || saving || hasForbiddenBinding}
            loading={saving}
            onClick={() => void saveAll()}
            variant="primary"
          >
            {t("common.saveChanges")}
          </IqujiButton>
        </div>
      }
      closeLabel={t("secrets.closeDrawer")}
      description={purposeLabel(asset.assetType, asset.purpose, t)}
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
      onOpenChange={onOpenChange}
      open
      size="md"
      title={asset.name}
    >
      <div className="ops-form-stack">
        <section className="ops-form-section">
          <h3 className="ops-form-section__title">{t("secrets.sectionMetaTitle")}</h3>
          <div className="ops-secret-meta-grid">
            <div className="ops-secret-meta-item">
              <span className="ops-secret-meta-item__label">{t("secrets.secretRef")}</span>
              <SecretCopySnippet label={t("secrets.secretRef")} value={asset.secretRef ?? ""} />
            </div>
            <div className="ops-secret-meta-item">
              <span className="ops-secret-meta-item__label">{t("secrets.maskedSummary")}</span>
              <SecretCopySnippet
                label={t("secrets.maskedSummary")}
                value={asset.maskedSummary ?? "—"}
              />
            </div>
            <div className="ops-secret-meta-item">
              <span className="ops-secret-meta-item__label">
                {t("secrets.materialFingerprint")}
              </span>
              <SecretCopySnippet
                label={t("secrets.materialFingerprint")}
                value={asset.fingerprint ?? ""}
              />
            </div>
            <div className="ops-secret-meta-item">
              <span className="ops-secret-meta-item__label">{t("secrets.latestRotatedTime")}</span>
              <span className="ops-secret-meta-item__text">
                {asset.rotatedAt
                  ? new Date(asset.rotatedAt).toLocaleString(locale)
                  : t("secrets.neverRotated")}
              </span>
            </div>
          </div>
        </section>

        <section className="ops-form-section">
          <h3 className="ops-form-section__title">{t("secrets.sectionBasicTitle")}</h3>
          <IqujiField label={t("common.name")} required>
            <IqujiInput onChange={(event) => setName(event.currentTarget.value)} value={name} />
          </IqujiField>
          <IqujiField label={t("secrets.remark")}>
            <IqujiInput
              onChange={(event) => setDescription(event.currentTarget.value)}
              placeholder={t("secrets.remarkPlaceholder")}
              value={description}
            />
          </IqujiField>
        </section>

        {asset.assetType === "ssh_key" ? (
          <section className="ops-form-section">
            <h3 className="ops-form-section__title">{t("secrets.sectionServerBinding")}</h3>
            {!materialReady ? (
              <IqujiAlert tone="info" title={t("operations.bindingMaterialNotReady")}>
                {t("operations.bindingCanUnbind")}
              </IqujiAlert>
            ) : null}
            <div className="ops-stack-xs">
              <IqujiMultiSelect
                emptyLabel={t("secrets.noAvailableServers")}
                label={t("secrets.boundServers")}
                onChange={(value) =>
                  setBoundIds(value.filter((id) => materialReady || originallyBound.has(id)))
                }
                options={servers.map((server) => ({
                  label: server.name,
                  value: server.id,
                  description: server.publicHost,
                  disabled: !materialReady && !originallyBound.has(server.id),
                }))}
                placeholder={t("secrets.selectServersPlaceholder")}
                value={boundIds}
              />
            </div>
          </section>
        ) : null}

        {registry || asset.assetType === "git_token" ? (
          <section className="ops-form-section">
            <div className="ops-form-section__header">
              <h3 className="ops-form-section__title">
                {t(registry ? "secrets.sectionRegistryApps" : "secrets.sectionGitApps")}
              </h3>
              <span className="ops-form-section__description">
                {t(registry ? "secrets.registryPurpose" : "secrets.gitAppsDesc")}
              </span>
            </div>
            {apps.length > 0 ? (
              <div className="ops-secret-tag-list">
                {apps.map((app) => (
                  <span className="ops-secret-tag" key={app}>
                    <IqujiIcon decorative name="rocket" size={12} />
                    {app}
                  </span>
                ))}
              </div>
            ) : (
              <p className="ops-muted ops-text-body-sm">
                {t(registry ? "secrets.noRegistryApps" : "secrets.noGitApps")}
              </p>
            )}
          </section>
        ) : null}
      </div>
    </IqujiDrawer>
  );
}

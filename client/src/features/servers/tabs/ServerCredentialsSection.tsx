import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiCheckbox,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { workspacePages } from "@/contracts/pages";
import type { SecretAsset, Server } from "@/contracts/types";
import {
  bindSecretToServer,
  fetchSecretAssets,
  SECRET_ASSETS_QUERY_KEY,
} from "@/features/secrets/api";
import { SshKeyFormDrawer } from "@/features/secrets/SshKeyFormDrawer";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { SurfacePanel } from "@/shared/ui/surface-panel";

/**
 * 服务器侧唯一的凭据入口：面板只读展示，绑定与新建 SSH 私钥都在此就地完成。
 */
export function ServerCredentialsSection({
  server,
  secrets,
  onChanged,
}: {
  server: Server;
  secrets: SecretAsset[];
  onChanged: () => void;
}) {
  const notify = useIqujiNotification();
  const navigate = useNavigate();
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [sshDrawerOpen, setSshDrawerOpen] = useState(false);
  const [selection, setSelection] = useState<string[]>([]);
  const assets = useQuery({ queryKey: SECRET_ASSETS_QUERY_KEY, queryFn: fetchSecretAssets });
  const bindable = (assets.data?.items ?? []).filter((asset) => asset.assetType === "ssh_key");
  const mutation = useMutation({
    mutationFn: (ids: string[]) => bindSecretToServer({ serverId: server.id, secretAssetIds: ids }),
    onSuccess: () => {
      setOpen(false);
      onChanged();
      notify.open({
        title: t("servers.credentialsUpdated"),
        description: t("servers.probeToVerify"),
        tone: "success",
      });
    },
    onError: (error) =>
      notify.open({
        title: t("servers.credentialsBindFailed"),
        description: errorMessage(error),
        tone: "danger",
        duration: 0,
      }),
  });

  const goToSecrets = () => {
    if (typeof window !== "undefined") window.location.hash = "secrets";
    void navigate({ to: workspacePages.settings.path });
  };

  return (
    <>
      <SurfacePanel
        actions={
          <>
            <IqujiButton compact onClick={() => setSshDrawerOpen(true)} variant="secondary">
              <IqujiIcon decorative name="plus" size={13} /> {t("servers.newSshKey")}
            </IqujiButton>
            <IqujiButton
              compact
              onClick={() => {
                setSelection(secrets.map((item) => item.id));
                setOpen(true);
              }}
              variant="secondary"
            >
              {t("servers.manageBindings")}
            </IqujiButton>
          </>
        }
        title={t("servers.credentialsSectionTitle")}
      >
        {secrets.length ? (
          <div className="ops-credential-list">
            {secrets.map((secret) => (
              <div className="ops-credential-card" key={secret.id}>
                <div className="ops-credential-card__info">
                  <div className="ops-credential-card__icon">
                    <IqujiIcon decorative name="key-round" size={16} />
                  </div>
                  <div className="ops-credential-card__names">
                    <span className="ops-credential-card__name">{secret.name}</span>
                    <span className="ops-credential-card__fingerprint">
                      {secret.fingerprint ?? secret.maskedSummary ?? t("servers.noFingerprint")}
                    </span>
                  </div>
                </div>
                <div className="ops-service-row__action">
                  <IqujiBadge tone={secret.materialStatus === "missing" ? "warning" : "success"}>
                    {secret.materialStatus === "missing"
                      ? t("servers.materialMissing")
                      : t("servers.availableForLogin")}
                  </IqujiBadge>
                  <IqujiButton compact onClick={goToSecrets} variant="ghost">
                    {t("servers.viewCredential")}
                  </IqujiButton>
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="ops-workload-empty-card">
            <div className="ops-workload-empty-icon">
              <IqujiIcon decorative name="key-round" size={18} />
            </div>
            <p className="ops-workload-empty-text">{t("servers.noCredentialsBound")}</p>
            <div className="ops-cluster-actions">
              <IqujiButton compact onClick={() => setSshDrawerOpen(true)} variant="primary">
                {t("servers.createAndBindKey")}
              </IqujiButton>
              <IqujiButton
                compact
                onClick={() => {
                  setSelection(secrets.map((item) => item.id));
                  setOpen(true);
                }}
                variant="secondary"
              >
                {t("servers.bindExistingCredential")}
              </IqujiButton>
            </div>
          </div>
        )}
      </SurfacePanel>

      <InspectorDrawer
        description={t("servers.manageCredentialsDesc")}
        footer={
          <>
            <IqujiButton
              disabled={mutation.isPending}
              onClick={() => setOpen(false)}
              variant="secondary"
            >
              {t("common.cancel")}
            </IqujiButton>
            <IqujiButton
              disabled={mutation.isPending}
              loading={mutation.isPending}
              onClick={() => mutation.mutate(selection)}
            >
              {t("servers.saveBinding")}
            </IqujiButton>
          </>
        }
        onClose={() => {
          if (!mutation.isPending) setOpen(false);
        }}
        open={open}
        title={t("servers.manageCredentialsTitle", { name: server.name })}
      >
        <div className="ops-form-stack">
          {assets.isError ? (
            <IqujiAlert title={t("servers.loadCredentialsFailed")} tone="danger">
              {errorMessage(assets.error)}
            </IqujiAlert>
          ) : null}
          {bindable.map((asset) => (
            <IqujiCheckbox
              checked={selection.includes(asset.id)}
              description={
                asset.materialStatus === "missing"
                  ? t("servers.materialMissingPrompt")
                  : (asset.fingerprint ?? asset.maskedSummary)
              }
              disabled={
                mutation.isPending ||
                (asset.materialStatus === "missing" && !selection.includes(asset.id))
              }
              key={asset.id}
              label={asset.name}
              onChange={(event) => {
                const checked = event.currentTarget.checked;
                setSelection((ids) =>
                  checked ? [...ids, asset.id] : ids.filter((id) => id !== asset.id),
                );
              }}
            />
          ))}
          {!bindable.length && !assets.isLoading ? (
            <IqujiAlert title={t("servers.noBindableKeys")} tone="info">
              {t("servers.noKeysRecorded")}
              <IqujiButton
                compact
                onClick={() => {
                  setOpen(false);
                  setSshDrawerOpen(true);
                }}
                variant="primary"
              >
                {t("servers.createNewKeyNow")}
              </IqujiButton>
            </IqujiAlert>
          ) : null}
        </div>
      </InspectorDrawer>

      <SshKeyFormDrawer
        defaultServerId={server.id}
        onOpenChange={(next) => {
          setSshDrawerOpen(next);
          if (!next) onChanged();
        }}
        open={sshDrawerOpen}
        servers={[server]}
      />
    </>
  );
}

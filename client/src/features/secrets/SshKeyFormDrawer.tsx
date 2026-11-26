import {
  IqujiAlert,
  IqujiButton,
  IqujiField,
  IqujiInput,
  IqujiSelect,
  IqujiTextarea,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import type { Server } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { FieldWithHelp } from "@/shared/ui/field-help";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { createSecretAsset, SECRET_ASSETS_QUERY_KEY } from "./api";
import { SecretCreationSession } from "./secret-creation-session";

export interface SshKeyFormDrawerProps {
  open: boolean;
  servers: Server[];
  defaultServerId?: string;
  onOpenChange: (open: boolean) => void;
}

/** 新建 SSH 私钥专有抽屉：录入部署私钥并可直接关联目标服务器。 */
export function SshKeyFormDrawer({
  open,
  servers,
  defaultServerId = "",
  onOpenChange,
}: SshKeyFormDrawerProps) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const operation = useConfirmedOperation();
  const creation = useRef(new SecretCreationSession());
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [secretValue, setSecretValue] = useState("");
  const [serverId, setServerId] = useState(defaultServerId);
  const [binding, setBinding] = useState(false);

  useEffect(() => {
    setSecretValue("");
    if (!open) return;
    setName("");
    setDescription("");
    setServerId(defaultServerId);
    setBinding(false);
    creation.current = new SecretCreationSession();
  }, [open, defaultServerId]);

  const submit = async () => {
    setBinding(true);
    try {
      const accepted = await creation.current.save({
        label: t("secrets.saveSshKeyLabel", { name: name.trim() }),
        submit: operation.submit,
        create: (requestKey) =>
          createSecretAsset(
            {
              assetType: "ssh_key",
              name: name.trim(),
              secretValue: secretValue.trim(),
              ...(serverId ? { bindServerId: serverId } : {}),
              ...(description.trim() ? { description: description.trim() } : {}),
            },
            requestKey,
          ),
      });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: SECRET_ASSETS_QUERY_KEY }),
        queryClient.invalidateQueries({ queryKey: ["servers"] }),
      ]);
      if (accepted) onOpenChange(false);
    } finally {
      setBinding(false);
    }
  };

  const runSubmit = async () => {
    try {
      await submit();
    } catch {
      // 保留已保存的凭据和原操作，重试不会再次创建。
    }
  };

  const locked = operation.isPending || binding || creation.current.hasAsset;
  const valid = name.trim().length > 0 && secretValue.trim().length > 0;

  const close = async () => {
    if (operation.isPending || binding) return;
    if (operation.operationId) await operation.cancelPending();
    onOpenChange(false);
  };

  return (
    <InspectorDrawer
      description={t("secrets.sshKeyDrawerDesc")}
      footer={
        <>
          <IqujiButton
            disabled={operation.isPending || binding}
            onClick={() => void close().catch(() => undefined)}
            variant="secondary"
          >
            {t("common.cancel")}
          </IqujiButton>
          <IqujiButton
            disabled={!valid || operation.isPending || binding || creation.current.rejected}
            loading={operation.isPending || binding}
            onClick={() => void runSubmit()}
          >
            {t("secrets.saveSshKeyAction")}
          </IqujiButton>
        </>
      }
      onClose={() => {
        void close().catch(() => undefined);
      }}
      open={open}
      size="md"
      title={t("secrets.sshKeyDrawerTitle")}
    >
      <div className="ops-form-stack">
        <div className="ops-form-hero">
          <div className="ops-form-hero__icon">
            <IqujiIcon decorative name="key-round" size={20} />
          </div>
          <div className="ops-form-hero__body">
            <div className="ops-form-hero__headline">
              <h4 className="ops-form-hero__title">{t("secrets.typeSshKey")}</h4>
            </div>
            <p className="ops-form-hero__desc">{t("secrets.purposeSsh")}</p>
          </div>
        </div>

        {creation.current.hasAsset ? (
          <IqujiAlert
            title={
              creation.current.rejected ? t("secrets.tokenNotFinished") : t("secrets.tokenSaved")
            }
            tone="warning"
          >
            {creation.current.rejected
              ? t("secrets.tokenNotFinishedDesc")
              : t("secrets.tokenSavedDesc")}
          </IqujiAlert>
        ) : null}

        <IqujiField label={t("secrets.sshKeyName")} required>
          <IqujiInput
            disabled={locked}
            onChange={(event) => setName(event.currentTarget.value)}
            placeholder={t("secrets.keyNamePlaceholder")}
            value={name}
          />
        </IqujiField>

        <FieldWithHelp help={t("secrets.targetServer")} label={t("secrets.boundServers")}>
          <IqujiSelect
            disabled={locked}
            onValueChange={(value) => setServerId(value ?? "")}
            options={[
              { label: t("secrets.targetServerNone"), value: "" },
              ...servers.map((server) => ({
                label: server.name,
                value: server.id,
                description: server.publicHost,
              })),
            ]}
            value={serverId}
          />
        </FieldWithHelp>

        <FieldWithHelp
          help={t("secrets.materialHelp")}
          label={t("secrets.sshPrivateKeyContent")}
          required
        >
          <IqujiTextarea
            autoComplete="off"
            className="ops-font-mono"
            disabled={locked}
            onChange={(event) => setSecretValue(event.currentTarget.value)}
            placeholder={t("secrets.privateKeyPlaceholder")}
            rows={7}
            spellCheck={false}
            value={secretValue}
          />
        </FieldWithHelp>

        <IqujiField label={t("secrets.remark")}>
          <IqujiInput
            disabled={locked}
            onChange={(event) => setDescription(event.currentTarget.value)}
            placeholder={t("secrets.remarkPlaceholder")}
            value={description}
          />
        </IqujiField>
      </div>
    </InspectorDrawer>
  );
}

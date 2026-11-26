import {
  IqujiAlert,
  IqujiButton,
  IqujiField,
  IqujiInput,
  IqujiTextarea,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import type { SecretAsset } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { FieldWithHelp } from "@/shared/ui/field-help";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { createSecretAsset, SECRET_ASSETS_QUERY_KEY } from "./api";
import { SecretCreationSession } from "./secret-creation-session";

export interface GitTokenFormDrawerProps {
  applicationSecret?: boolean;
  registrySecret?: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated?: (asset: SecretAsset) => void;
}

export function GitTokenFormDrawer({
  open,
  onOpenChange,
  onCreated,
  applicationSecret: appCredential = false,
  registrySecret = false,
}: GitTokenFormDrawerProps) {
  const { t } = useI18n();
  const applicationSecret = appCredential || registrySecret;
  const secretTitle = t(
    registrySecret ? "secrets.newRegistrySecret" : "secrets.newApplicationSecret",
  );
  const secretPurpose = t(
    registrySecret ? "secrets.registryPurpose" : "secrets.purposeApplicationSecret",
  );
  const typeLabel = t(
    registrySecret ? "secrets.typeRegistrySecret" : "secrets.typeApplicationSecret",
  );
  const queryClient = useQueryClient();
  const operation = useConfirmedOperation();
  const creation = useRef(new SecretCreationSession());
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [secretValue, setSecretValue] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setSecretValue("");
    if (!open) return;
    setName("");
    setDescription("");
    setSaving(false);
    creation.current = new SecretCreationSession();
  }, [open]);

  const submit = async () => {
    setSaving(true);
    try {
      let createdAsset: SecretAsset | null = null;
      const accepted = await creation.current.save({
        label: applicationSecret
          ? secretTitle
          : t("secrets.saveGitTokenLabel", { name: name.trim() }),
        submit: operation.submit,
        create: async (requestKey) => {
          const res = await createSecretAsset(
            {
              assetType: registrySecret
                ? "registry_secret"
                : applicationSecret
                  ? "application_secret"
                  : "git_token",
              name: name.trim(),
              secretValue: applicationSecret ? secretValue : secretValue.trim(),
              ...(description.trim() ? { description: description.trim() } : {}),
            },
            requestKey,
          );
          createdAsset = res.asset;
          return res;
        },
      });
      await queryClient.invalidateQueries({ queryKey: SECRET_ASSETS_QUERY_KEY });
      if (accepted) {
        if (createdAsset && onCreated) {
          onCreated(createdAsset);
        }
        onOpenChange(false);
      }
    } finally {
      setSaving(false);
    }
  };

  const runSubmit = async () => {
    try {
      await submit();
    } catch {
      // 保留已保存的凭据和原操作，重试不会再次创建。
    }
  };

  const locked = operation.isPending || saving || creation.current.hasAsset;
  const valid = name.trim().length > 0 && secretValue.trim().length > 0;

  const close = async () => {
    if (operation.isPending || saving) return;
    if (operation.operationId) await operation.cancelPending();
    onOpenChange(false);
  };

  return (
    <InspectorDrawer
      description={applicationSecret ? secretPurpose : t("secrets.gitTokenDrawerDesc")}
      footer={
        <>
          <IqujiButton
            disabled={operation.isPending || saving}
            onClick={() => void close().catch(() => undefined)}
            variant="secondary"
          >
            {t("common.cancel")}
          </IqujiButton>
          <IqujiButton
            disabled={!valid || operation.isPending || saving || creation.current.rejected}
            loading={operation.isPending || saving}
            onClick={() => void runSubmit()}
          >
            {applicationSecret ? t("common.save") : t("secrets.saveGitTokenAction")}
          </IqujiButton>
        </>
      }
      onClose={() => {
        void close().catch(() => undefined);
      }}
      open={open}
      size="md"
      title={applicationSecret ? secretTitle : t("secrets.gitTokenDrawerTitle")}
    >
      <div className="ops-form-stack">
        <div className="ops-form-hero">
          <div className="ops-form-hero__icon">
            <IqujiIcon decorative name="code" size={20} />
          </div>
          <div className="ops-form-hero__body">
            <div className="ops-form-hero__headline">
              <h4 className="ops-form-hero__title">
                {applicationSecret ? typeLabel : t("secrets.gitTokenHeadline")}
              </h4>
            </div>
            <p className="ops-form-hero__desc">
              {applicationSecret ? secretPurpose : t("secrets.gitTokenHeadlineDesc")}
            </p>
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

        <IqujiField label={t("secrets.keyName")} required>
          <IqujiInput
            disabled={locked}
            onChange={(event) => setName(event.currentTarget.value)}
            placeholder={applicationSecret ? "" : t("secrets.tokenNamePlaceholder")}
            value={name}
          />
        </IqujiField>

        <FieldWithHelp
          help={applicationSecret ? secretPurpose : t("secrets.tokenFieldDesc")}
          label={applicationSecret ? t("secrets.credentialValue") : t("secrets.tokenFieldLabel")}
          required
        >
          <IqujiTextarea
            autoComplete="off"
            className="ops-font-mono"
            disabled={locked}
            onChange={(event) => setSecretValue(event.currentTarget.value)}
            placeholder={applicationSecret ? "" : t("secrets.tokenFieldPlaceholder")}
            rows={4}
            spellCheck={false}
            value={secretValue}
          />
        </FieldWithHelp>

        <IqujiField label={t("secrets.remark")}>
          <IqujiInput
            disabled={locked}
            onChange={(event) => setDescription(event.currentTarget.value)}
            placeholder={applicationSecret ? "" : t("secrets.tokenRemarkPlaceholder")}
            value={description}
          />
        </IqujiField>
      </div>
    </InspectorDrawer>
  );
}

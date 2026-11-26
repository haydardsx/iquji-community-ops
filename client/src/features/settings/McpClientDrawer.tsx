import {
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiDiagnosticBanner,
  IqujiField,
  IqujiInput,
} from "@iquji-community/web-component";
import { useEffect, useState } from "react";
import { useWorkspaceUnloadGuard } from "@/shared/desktop/workspace-interactions";
import { useI18n } from "@/shared/i18n";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { RecordSnippet } from "@/shared/ui/record-snippet";
import { limits } from "../../../../server/internal/automation/commands.json";
import { AutomationPermissions } from "./AutomationPermissions";
import {
  type Client,
  type Command,
  type Permission,
  policyDraft,
  policyPayload,
  type Rule,
  summarizeDraft,
} from "./automation-permissions";

type McpClientDrawerProps = {
  open: boolean;
  client: Client | null;
  commands: Command[];
  busy: boolean;
  error: string;
  onClose: () => void;
  onSave: (payload: {
    id?: string;
    name: string;
    permissions: Permission[];
    rules: Rule[];
  }) => Promise<void>;
};

/** 新建或编辑 MCP 客户端的权限配置抽屉 */
export function McpClientDrawer({
  open,
  client,
  commands,
  busy,
  error,
  onClose,
  onSave,
}: McpClientDrawerProps) {
  const { t } = useI18n();
  const [name, setName] = useState(() => client?.name ?? "");
  const [draft, setDraft] = useState(() => policyDraft(client ?? undefined));
  const [dirty, setDirty] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);

  useEffect(() => {
    if (!open) return;
    setName(client?.name ?? "");
    setDraft(policyDraft(client ?? undefined));
    setDirty(false);
    setConfirmDiscard(false);
  }, [open, client]);

  useWorkspaceUnloadGuard(open && (dirty || busy));

  const policy = policyPayload(draft);
  const stats = summarizeDraft(draft, commands);
  const trimmedName = name.trim();
  const nameTooLong = new TextEncoder().encode(trimmedName).length > limits.maxClientNameBytes;
  const saveDisabled =
    busy || !trimmedName || nameTooLong || !policy.valid || (Boolean(client) && !dirty);

  const handleRequestClose = () => {
    if (busy) return;
    if (dirty) {
      setConfirmDiscard(true);
      return;
    }
    onClose();
  };

  const handleSubmit = async () => {
    if (saveDisabled) return;
    await onSave({
      ...(client ? { id: client.id } : {}),
      name: trimmedName,
      permissions: policy.permissions,
      rules: policy.rules,
    });
  };

  return (
    <>
      <InspectorDrawer
        footer={
          <>
            <IqujiButton disabled={busy} onClick={handleRequestClose} variant="secondary">
              {t("common.cancel")}
            </IqujiButton>
            <IqujiButton
              disabled={saveDisabled}
              loading={busy}
              onClick={() => void handleSubmit()}
              variant="primary"
            >
              {client ? t("automation.savePermissions") : t("automation.grant")}
            </IqujiButton>
          </>
        }
        headerMeta={client ? <RecordSnippet label="ID" value={client.id} /> : undefined}
        onClose={handleRequestClose}
        open={open}
        size="lg"
        title={client ? t("automation.editClient") : t("automation.newClient")}
      >
        <div className="ops-form-stack">
          {error ? <IqujiDiagnosticBanner title={error} tone="danger" /> : null}
          {!policy.valid ? (
            <IqujiDiagnosticBanner compact title={t("automation.invalidScope")} tone="warning" />
          ) : null}

          <section className="ops-form-section">
            <IqujiField id="ops-mcp-client-name" label={t("automation.client")} required>
              <IqujiInput
                autoFocus={!client}
                disabled={busy}
                id="ops-mcp-client-name"
                onChange={(e) => {
                  setName(e.currentTarget.value);
                  setDirty(true);
                }}
                placeholder={t("automation.clientPlaceholder")}
                value={name}
              />
            </IqujiField>
          </section>

          <section className="ops-form-section">
            <div className="ops-mcp-drawer-tools-head">
              <h3 className="ops-form-section__title">{t("automation.toolsSectionTitle")}</h3>
              <IqujiBadge tone={stats.granted > 0 ? "info" : "neutral"}>
                {t("automation.grantedRatio", { granted: stats.granted, total: stats.total })}
              </IqujiBadge>
            </div>
            <AutomationPermissions
              commands={commands}
              disabled={busy}
              draft={draft}
              key={client?.id ?? "new"}
              setDraft={(next) => {
                setDraft(next);
                setDirty(true);
              }}
            />
          </section>
        </div>
      </InspectorDrawer>

      <IqujiConfirmDialog
        cancelLabel={t("common.continueEditing")}
        closeLabel={t("common.close")}
        confirmLabel={t("common.discardChanges")}
        description={t("common.discardConfirmDesc")}
        onConfirm={() => {
          setConfirmDiscard(false);
          onClose();
        }}
        onOpenChange={setConfirmDiscard}
        open={confirmDiscard}
        title={t("common.discardConfirmTitle")}
      />
    </>
  );
}

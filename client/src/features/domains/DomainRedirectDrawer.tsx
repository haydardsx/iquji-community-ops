import {
  IqujiAlert,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiField,
  IqujiSelect,
} from "@iquji-community/web-component";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import type { Domain, DomainRedirect, Server } from "@/contracts/types";
import { operationErrorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { useOperationError, useOperationNotice } from "@/shared/ui/use-operation-feedback";
import { runDomainRedirect, saveDomainRedirect } from "./api";

export function DomainRedirectDrawer({
  source,
  domains,
  servers,
  onClose,
  onSaved,
}: {
  source: Domain;
  domains: Domain[];
  servers: Server[];
  onClose: () => void;
  onSaved: () => Promise<unknown>;
}) {
  const { t } = useI18n();
  const notice = useOperationNotice();
  const [draft, setDraft] = useState<DomainRedirect>(
    () =>
      source.redirect ?? {
        gatewayServerId: servers.length === 1 ? servers[0].id : "",
        targetDomainId: source.certificateId ? source.id : "",
        scheme: "https",
        mode: "permanent",
      },
  );
  const [action, setAction] = useState<"apply" | "remove" | null>(null);
  const confirmed = useConfirmedOperation();
  const save = useMutation({
    mutationFn: () => saveDomainRedirect(source.id, draft),
    onSuccess: async () => {
      await onSaved();
      notice(t("domains.redirectSaved"));
    },
  });
  const run = useMutation({
    mutationFn: (kind: "apply" | "remove") =>
      confirmed.submit({
        action: kind === "apply" ? "apply_domain_redirect" : "remove_domain_redirect",
        targetKind: "domain",
        targetId: source.id,
        label: t(kind === "apply" ? "domains.redirectPublish" : "domains.redirectRemove"),
        create: (key) => runDomainRedirect(source.id, kind, key),
      }),
    onSuccess: async () => {
      setAction(null);
      await onSaved();
      onClose();
    },
  });
  useOperationError(save.error, t("domains.saveFailed"));
  const saved = source.redirect;
  const changed =
    !saved ||
    draft.gatewayServerId !== saved.gatewayServerId ||
    draft.targetDomainId !== saved.targetDomainId ||
    draft.scheme !== saved.scheme ||
    draft.mode !== saved.mode;
  const targetValid = domains.some((asset) => asset.id === draft.targetDomainId);
  const valid =
    targetValid &&
    servers.some((server) => server.id === draft.gatewayServerId) &&
    (draft.targetDomainId !== source.id ||
      (draft.scheme === "https" && Boolean(source.certificateId)));
  const busy = save.isPending || run.isPending;
  return (
    <>
      <InspectorDrawer
        open
        onClose={onClose}
        size="md"
        title={t("domains.redirect")}
        description={source.name}
        footer={
          <>
            {saved ? (
              <IqujiButton variant="danger" disabled={busy} onClick={() => setAction("remove")}>
                {t("domains.redirectRemove")}
              </IqujiButton>
            ) : null}
            <IqujiButton
              variant="secondary"
              disabled={!changed || !valid || busy}
              loading={save.isPending}
              onClick={() => save.mutate()}
            >
              {t("common.save")}
            </IqujiButton>
            <IqujiButton disabled={changed || !valid || busy} onClick={() => setAction("apply")}>
              {t("domains.redirectPublish")}
            </IqujiButton>
          </>
        }
      >
        <div className="ops-form-stack">
          <IqujiField label={t("domains.redirectTarget")} required>
            <IqujiSelect
              value={draft.targetDomainId}
              options={domains
                .filter((asset) => asset.id !== source.id || source.certificateId)
                .map((asset) => ({ value: asset.id, label: asset.name }))}
              onValueChange={(value) =>
                setDraft({
                  ...draft,
                  targetDomainId: value ?? "",
                  scheme: value === source.id ? "https" : draft.scheme,
                })
              }
            />
          </IqujiField>
          <IqujiField label={t("domains.redirectScheme")} required>
            <IqujiSelect
              value={draft.scheme}
              options={
                draft.targetDomainId === source.id
                  ? [{ value: "https", label: "HTTPS" }]
                  : [
                      { value: "https", label: "HTTPS" },
                      { value: "http", label: "HTTP" },
                    ]
              }
              onValueChange={(value) =>
                setDraft({ ...draft, scheme: value === "http" ? "http" : "https" })
              }
            />
          </IqujiField>
          <IqujiField label={t("domains.redirectGateway")} required>
            <IqujiSelect
              value={draft.gatewayServerId}
              options={servers.map((server) => ({ value: server.id, label: server.name }))}
              onValueChange={(value) => setDraft({ ...draft, gatewayServerId: value ?? "" })}
            />
          </IqujiField>
          <IqujiField label={t("domains.redirectMode")} required>
            <IqujiSelect
              value={draft.mode}
              options={[
                { value: "permanent", label: t("domains.redirectPermanent") },
                { value: "temporary", label: t("domains.redirectTemporary") },
              ]}
              onValueChange={(value) =>
                setDraft({ ...draft, mode: value === "temporary" ? "temporary" : "permanent" })
              }
            />
          </IqujiField>
          {!servers.length ? (
            <IqujiAlert tone="warning" title={t("domains.redirectNoGateway")} />
          ) : null}
          {source.redirectRuntime?.lastErrorCode ? (
            <IqujiAlert tone="danger" title={t("domains.redirectFailed")}>
              {operationErrorMessage(source.redirectRuntime.lastErrorCode, undefined, t)}
            </IqujiAlert>
          ) : null}
        </div>
      </InspectorDrawer>
      <IqujiConfirmDialog
        open={action !== null}
        closeLabel={t("common.close")}
        onOpenChange={(open) => {
          if (!open) setAction(null);
        }}
        title={t(action === "remove" ? "domains.redirectRemove" : "domains.redirectPublish")}
        description={t(
          action === "remove" ? "domains.redirectRemoveConfirm" : "domains.redirectApplyConfirm",
          { name: source.name },
        )}
        confirmLabel={t("common.confirm")}
        cancelLabel={t("common.cancel")}
        confirmLoading={run.isPending}
        onConfirm={() => {
          if (action) run.mutate(action);
        }}
      />
    </>
  );
}

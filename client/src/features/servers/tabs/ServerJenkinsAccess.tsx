import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiInput,
  IqujiSelect,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { Server, ServerRuntimeMetrics } from "@/contracts/types";
import { fetchDomains } from "@/features/domains/api";
import { errorMessage } from "@/shared/api/client";
import {
  useAutomationForm,
  useAutomationOpener,
  useAutomationScope,
} from "@/shared/desktop/automation-controls";
import { openExternalUrl } from "@/shared/desktop/external-url";
import { useI18n } from "@/shared/i18n";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { ActionGuideCard } from "@/shared/tutorial/ActionGuideOverlay";
import { FieldWithHelp } from "@/shared/ui/field-help";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import {
  bindJenkinsAccess,
  fetchJenkinsAccess,
  unbindJenkinsAccess,
  validViewerPassword,
  verifiedJenkinsURL,
} from "../jenkins-access";
import "./ServerJenkinsAccess.css";

function statusTone(status?: string): "success" | "info" | "danger" | "neutral" {
  switch (status) {
    case "active":
      return "success";
    case "pending":
      return "info";
    case "failed":
      return "danger";
    default:
      return "neutral";
  }
}

export function ServerJenkinsAccess({
  onOpenChange,
  open,
  serverId,
  servers,
}: {
  serverId: string;
  servers: Server[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useI18n();
  const notify = useIqujiNotification();
  const operation = useConfirmedOperation();
  const [mode, setMode] = useState<"bind" | "unbind" | null>(null);
  const [confirmUnbind, setConfirmUnbind] = useState(false);
  const [domainId, setDomainId] = useState("");
  const [gatewayServerId, setGatewayId] = useState("");
  const [viewerPassword, setPassword] = useState("");
  const [copied, setCopied] = useState(false);

  const access = useQuery({
    queryKey: ["server-jenkins-access", serverId],
    queryFn: () => fetchJenkinsAccess(serverId),
    refetchInterval: (query) => (query.state.data?.status === "pending" ? 2000 : false),
  });
  const domains = useQuery({
    queryKey: ["domains"],
    queryFn: fetchDomains,
    enabled: open,
  });

  const data = access.data;
  const url = verifiedJenkinsURL(data);
  const pending = data?.status === "pending";
  const editing = mode === "bind";
  const label = t(editing ? "servers.bindJenkins" : "servers.unbindJenkins");
  const locked = operation.isPending || Boolean(operation.operationId);

  useEffect(() => {
    if (open) {
      setDomainId(data?.domainId || "");
      setGatewayId(data?.gatewayServerId || "");
      setPassword("");
      setMode("bind");
    } else {
      setMode(null);
      setConfirmUnbind(false);
      setPassword("");
    }
  }, [open, data?.domainId, data?.gatewayServerId]);

  const close = () => {
    setMode(null);
    setConfirmUnbind(false);
    setPassword("");
    operation.reset();
    onOpenChange(false);
  };

  function openMode(targetMode: "bind" | "unbind") {
    setDomainId(data?.domainId || "");
    setGatewayId(data?.gatewayServerId || "");
    setPassword("");
    setMode(targetMode);
    if (targetMode === "unbind") {
      setConfirmUnbind(true);
    }
    onOpenChange(true);
  }

  const domainOptions = (domains.data?.items ?? [])
    .filter((item) => item.certificateId && !item.redirect)
    .map((item) => ({ label: item.name, value: item.id }));
  const gatewayOptions = servers
    .filter((item) => (item.health?.metrics as ServerRuntimeMetrics | undefined)?.traefik === true)
    .map((item) => ({ label: item.name, value: item.id }));

  const formID = editing ? "server.jenkins.bind" : "server.jenkins.unbind";
  const bindTarget = `server.jenkins.bind/${serverId}`;
  const unbindTarget = `server.jenkins.unbind/${serverId}`;
  const mayBind = Boolean(data?.ready && !pending);
  const mayUnbind = Boolean(data && data.status !== "unbound" && !pending);

  async function submit() {
    if (
      !mode ||
      operation.isPending ||
      (!operation.operationId && !(editing ? mayBind : mayUnbind))
    )
      throw Error("AUTOMATION_OBJECT_UNAVAILABLE");
    if (editing && !operation.operationId) {
      if (!validViewerPassword(viewerPassword)) throw Error("AUTOMATION_MANUAL_INPUT_REQUIRED");
      if (
        !domainOptions.some((item) => item.value === domainId) ||
        !gatewayOptions.some((item) => item.value === gatewayServerId)
      )
        throw Error("AUTOMATION_FORM_INVALID");
    }
    const run = await operation.submit({
      action: editing ? "bind_jenkins_access" : "unbind_jenkins_access",
      targetKind: "server",
      targetId: serverId,
      label,
      create: (requestKey) =>
        editing
          ? bindJenkinsAccess(serverId, { domainId, gatewayServerId, viewerPassword }, requestKey)
          : unbindJenkinsAccess(serverId, requestKey),
    });
    close();
    void access.refetch();
    return run;
  }

  useAutomationScope(
    "server.jenkins.access",
    serverId,
    "runtime",
    undefined,
    undefined,
    data ? { status: data.status, ready: data.ready } : undefined,
  );
  useAutomationOpener(bindTarget, () => openMode("bind"), mayBind);
  useAutomationOpener(unbindTarget, () => openMode("unbind"), mayUnbind);

  function setSelection(options: { value: string }[], value: string, set: (v: string) => void) {
    if (locked || !options.some((item) => item.value === value))
      throw Error("AUTOMATION_OBJECT_UNAVAILABLE");
    set(value);
  }

  useAutomationForm(
    formID,
    open && mode !== null,
    serverId,
    editing ? { domainId, gatewayServerId } : {},
    editing
      ? {
          domainId: (value) => setSelection(domainOptions, value, setDomainId),
          gatewayServerId: (value) => setSelection(gatewayOptions, value, setGatewayId),
        }
      : {},
    () => {
      if (locked) throw Error("AUTOMATION_OBJECT_UNAVAILABLE");
      close();
    },
    submit,
    { operationReceipt: true, manualFields: editing ? ["viewerPassword"] : undefined },
  );

  async function copyUrl(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      notify.open({ title: t("common.copyContent"), tone: "danger" });
    }
  }

  const isFormValid =
    domainOptions.some((item) => item.value === domainId) &&
    gatewayOptions.some((item) => item.value === gatewayServerId) &&
    validViewerPassword(viewerPassword);

  const confirmDisabled = locked || (editing && !operation.operationId && !isFormValid);

  const confirmText = operation.operationId
    ? t("servers.retryConfirm")
    : data?.status === "unbound"
      ? t("servers.bindJenkins")
      : t("servers.updateJenkinsAccess");

  return (
    <>
      <InspectorDrawer
        open={open}
        size="md"
        onClose={() => {
          if (!locked) close();
        }}
        title={t("servers.jenkinsAccess")}
        description={t("servers.bindJenkinsHint")}
        headerMeta={
          <IqujiBadge tone={statusTone(data?.status)}>
            {t(`servers.jenkinsAccess.${data?.status ?? "unbound"}`)}
          </IqujiBadge>
        }
        footer={
          <div className="ops-drawer-footer-actions">
            <div className="ops-drawer-footer-actions__left">
              {data && data.status !== "unbound" && (
                <IqujiButton
                  variant="danger"
                  disabled={locked || pending}
                  data-ops-guide-open={unbindTarget}
                  onClick={() => {
                    setMode("unbind");
                    setConfirmUnbind(true);
                  }}
                >
                  {t("servers.unbindJenkins")}
                </IqujiButton>
              )}
            </div>
            <div className="ops-drawer-footer-actions__right">
              {operation.operationId && (
                <IqujiButton
                  variant="secondary"
                  disabled={operation.isPending}
                  onClick={() =>
                    void operation
                      .cancelPending()
                      .then(close)
                      .catch(() => {})
                  }
                >
                  {t("servers.cancelOriginalOp")}
                </IqujiButton>
              )}
              <IqujiButton
                variant="secondary"
                disabled={locked}
                data-ops-guide-command="close"
                onClick={close}
              >
                {t("common.cancel")}
              </IqujiButton>
              <IqujiButton
                variant="primary"
                disabled={confirmDisabled}
                loading={operation.isPending}
                onClick={() =>
                  void submit().catch(() => {
                    /* 统一操作通知保留重试入口。 */
                  })
                }
              >
                <span data-ops-guide-command="submit">{confirmText}</span>
              </IqujiButton>
            </div>
          </div>
        }
      >
        <div className="ops-form-stack">
          {access.isError && (
            <IqujiAlert tone="danger" title={t("servers.opStatusFailed")}>
              {errorMessage(access.error)}
            </IqujiAlert>
          )}

          {url && (
            <div className="ops-jenkins-access-hero">
              <div className="ops-jenkins-access-hero__lead">
                <span className="ops-jenkins-access-hero__icon">
                  <IqujiIcon decorative name="external-link" size={15} />
                </span>
                <span className="ops-jenkins-access-hero__url" title={url}>
                  {url}
                </span>
              </div>
              <div className="ops-jenkins-access-hero__actions">
                <IqujiButton compact variant="secondary" onClick={() => void copyUrl(url)}>
                  <IqujiIcon decorative name={copied ? "check" : "copy"} size={13} />
                  {copied ? t("common.copied") : t("common.copy")}
                </IqujiButton>
                <IqujiButton
                  compact
                  variant="primary"
                  onClick={async () => {
                    if (!(await openExternalUrl(url)))
                      notify.open({ title: t("servers.jenkinsOpenFailed"), tone: "danger" });
                  }}
                >
                  {t("servers.openJenkins")}
                </IqujiButton>
              </div>
            </div>
          )}

          {data && (
            <div className="ops-jenkins-access-meta">
              {data.viewerUsername && (
                <span>
                  {t("servers.jenkinsViewerAccount")}: {data.viewerUsername}
                </span>
              )}
              {data.version && (
                <>
                  <span className="ops-jenkins-access-meta__dot">·</span>
                  <span>Jenkins {data.version}</span>
                </>
              )}
              {data.checkedAt && (
                <>
                  <span className="ops-jenkins-access-meta__dot">·</span>
                  <span>
                    {t("servers.jenkinsLastChecked")}: {new Date(data.checkedAt).toLocaleString()}
                  </span>
                </>
              )}
            </div>
          )}

          {pending && <IqujiAlert tone="info" title={t("servers.jenkinsAccess.pending")} />}

          <div className="ops-form-stack" data-ops-guide-form={formID}>
            {mode && <ActionGuideCard form={formID} />}
            {editing && (
              <>
                <FieldWithHelp label={t("servers.jenkinsDomain")}>
                  <IqujiSelect
                    disabled={locked}
                    value={domainId}
                    data-ops-guide-field="domainId"
                    options={domainOptions}
                    onValueChange={(v) => setDomainId(v ?? "")}
                    placeholder={t("servers.selectDomain")}
                  />
                </FieldWithHelp>
                {domains.isError && (
                  <IqujiAlert title={t("servers.opStatusFailed")} tone="danger">
                    {errorMessage(domains.error)}
                  </IqujiAlert>
                )}
                <FieldWithHelp label={t("servers.jenkinsGateway")}>
                  <IqujiSelect
                    disabled={locked}
                    value={gatewayServerId}
                    data-ops-guide-field="gatewayServerId"
                    options={gatewayOptions}
                    onValueChange={(v) => setGatewayId(v ?? "")}
                    placeholder={t("servers.selectGateway")}
                  />
                </FieldWithHelp>
                <FieldWithHelp
                  label={t("servers.jenkinsPassword")}
                  help={t("servers.jenkinsPasswordHelp")}
                >
                  <IqujiInput
                    data-ops-guide-field="viewerPassword"
                    type="password"
                    disabled={locked}
                    value={viewerPassword}
                    autoComplete="new-password"
                    placeholder={
                      data?.status !== "unbound"
                        ? t("servers.jenkinsAccessPasswordUpdatePlaceholder")
                        : t("servers.jenkinsAccessPasswordPlaceholder")
                    }
                    onChange={(e) => setPassword(e.currentTarget.value)}
                  />
                </FieldWithHelp>
              </>
            )}
          </div>
        </div>
      </InspectorDrawer>

      <IqujiConfirmDialog
        open={confirmUnbind}
        title={t("servers.unbindJenkins")}
        description={t("servers.unbindJenkinsHint")}
        closeLabel={t("common.cancel")}
        mandatory={Boolean(operation.operationId)}
        onOpenChange={(next) => {
          if (!next && !locked) {
            setConfirmUnbind(false);
            setMode("bind");
          }
        }}
        confirmLabel={
          <span data-ops-guide-command="submit">
            {operation.operationId ? t("servers.retryConfirm") : t("servers.unbindJenkins")}
          </span>
        }
        cancelLabel={<span data-ops-guide-command="close">{t("common.cancel")}</span>}
        confirmLoading={operation.isPending}
        onConfirm={() =>
          void submit().catch(() => {
            /* 统一操作通知保留重试入口。 */
          })
        }
      >
        <div data-ops-guide-form="server.jenkins.unbind">
          {mode === "unbind" && <ActionGuideCard form="server.jenkins.unbind" />}
        </div>
      </IqujiConfirmDialog>
    </>
  );
}

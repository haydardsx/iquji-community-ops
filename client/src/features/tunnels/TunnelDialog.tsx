import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiField,
  IqujiInput,
  IqujiSelect,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import type { FrpTunnel, Server } from "@/contracts/types";
import { fetchDomains } from "@/features/domains/api";
import { errorMessage } from "@/shared/api/client";
import { useAutomationForm } from "@/shared/desktop/automation-controls";
import { useI18n } from "@/shared/i18n";
import { toOperationRun } from "@/shared/operations/task-response";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { ActionGuideCard } from "@/shared/tutorial/ActionGuideOverlay";
import { FieldWithHelp } from "@/shared/ui/field-help";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { createTunnel, previewTunnel, updateTunnel } from "./api";
import {
  applyEntrypoint,
  createDefaultMapping,
  createProbeMapping,
  MAPPING_LIMIT,
  mappingToInput,
  tunnelServerBlockReason,
  tunnelServerLabel,
} from "./mappings";
import { TunnelMappingFields } from "./TunnelMappingFields";
import type { FrpTunnelDetail, FrpTunnelMapping, TunnelDraft, TunnelMappingInput } from "./types";
import { tunnelBlockReasonLabel } from "./utils";

type MappingRow = { id: string; value: TunnelMappingInput };

const PREFLIGHT_CAPABILITIES: Record<string, string> = {
  frpc: "frpc",
  ssh: "SSH",
  docker: "Docker",
  swarm: "Swarm",
  frps: "frps",
  traefik: "Traefik",
};

export function TunnelDialog({
  activeServerIds,
  open,
  servers,
  tunnel,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  servers: Server[];
  tunnel?: FrpTunnel | FrpTunnelDetail | null;
  activeServerIds: Set<string>;
  onOpenChange: (open: boolean) => void;
  onSaved: (tunnelId?: string) => void;
}) {
  const isEdit = Boolean(tunnel);
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const operation = useConfirmedOperation();
  const resetOperation = operation.reset;
  const sequence = useRef(0);
  const toRows = (values: TunnelMappingInput[]): MappingRow[] =>
    values.map((value) => {
      sequence.current += 1;
      return { id: `mapping-${sequence.current}`, value };
    });
  const [name, setName] = useState("");
  const [serverId, setServerId] = useState("");
  const [frpsBindPort, setFrpsBindPort] = useState(7000);
  const [rows, setRows] = useState<MappingRow[]>(() => [
    { id: "mapping-0", value: createDefaultMapping() },
  ]);
  const domains = useQuery({ enabled: open, queryFn: fetchDomains, queryKey: ["domains"] });
  const domainNames = domains.data?.items.map((domain) => domain.name).sort() ?? [];
  const updateMutation = useMutation({
    mutationFn: ({ id, draft }: { id: string; draft: TunnelDraft }) => updateTunnel(id, draft),
    onSuccess: (updated) => {
      onSaved(updated.id);
      onOpenChange(false);
    },
  });
  const mappings = rows.map((row) => row.value);
  const draft = { frpsBindPort, mappings, name: name.trim(), serverId };
  const refreshServerStatus = (id: string) =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: ["servers"] }),
      queryClient.invalidateQueries({ queryKey: ["server-health", id] }),
    ]);
  const previewMutation = useMutation({
    mutationFn: (request: TunnelDraft) => previewTunnel(request, tunnel?.id),
    onSuccess: (_, request) => refreshServerStatus(request.serverId),
  });

  const serverOptions = servers.map((server) => {
    const reason = tunnelServerBlockReason(server, activeServerIds);
    const label = tunnelServerLabel(server, t);
    return {
      disabled: Boolean(reason),
      label: reason ? `${label} · ${t(reason)}` : label,
      value: server.id,
    };
  });
  const firstAvailableServerId = serverOptions.find((option) => !option.disabled)?.value;
  const selectedServer = serverOptions.find((option) => option.value === serverId);
  const serverEligible = Boolean(selectedServer && !selectedServer.disabled);
  // 编辑可保留已有的服务器绑定，重新选择时必须使用已核验的服务器。
  const keepsBinding = Boolean(selectedServer && tunnel?.serverId === serverId);

  // 只在打开或目标切换时重置表单，资产和服务器列表刷新不覆盖输入。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 依赖刻意收窄
  useEffect(() => {
    if (!open) return;
    previewMutation.reset();
    resetOperation();
    updateMutation.reset();
    if (tunnel) {
      setName(tunnel.name);
      setServerId(tunnel.serverId);
      setFrpsBindPort(tunnel.frpsBindPort || 7000);
      const existing = (tunnel.mappings as unknown as FrpTunnelMapping[]) ?? [];
      setRows(toRows(existing.length ? existing.map(mappingToInput) : [createDefaultMapping()]));
      return;
    }
    setName("");
    setServerId(firstAvailableServerId ?? "");
    setFrpsBindPort(7000);
    setRows(toRows([createDefaultMapping()]));
  }, [open, tunnel]);

  useEffect(() => {
    if (open && !tunnel && firstAvailableServerId) {
      setServerId((current) => current || firstAvailableServerId);
    }
  }, [open, tunnel, firstAvailableServerId]);
  const preview = previewMutation.data;

  const blockedByPreview = preview?.process?.decision?.allowed === false;
  const capabilityChecks = (preview?.process?.checks ?? []).filter(
    (check) => check.details?.capability && PREFLIGHT_CAPABILITIES[check.details.capability],
  );
  const canSubmit =
    name.trim().length > 0 &&
    (serverEligible || keepsBinding) &&
    mappings.length > 0 &&
    mappings.every(
      (mapping) =>
        mapping.name.trim().length > 0 &&
        mapping.frpsProxyPort >= 1024 &&
        (mapping.targetType === "http_probe" || mapping.localPort > 0) &&
        (mapping.publicDomain
          ? domainNames.includes(mapping.publicDomain)
          : mapping.publicScheme !== "https"),
    ) &&
    !blockedByPreview;

  const save = async () => {
    if (isEdit && tunnel) {
      await updateMutation.mutateAsync({ id: tunnel.id, draft });
      return;
    }
    let createdID: string | undefined;
    await operation
      .submit({
        action: "start_frp_tunnel",
        targetKind: "frp_tunnel_run",
        label: t("tunnels.createAndStartAction"),
        create: async (requestKey) => {
          const created = await createTunnel(draft, requestKey);
          createdID = created.tunnel.id;
          return toOperationRun(created.task);
        },
      })
      .then(() => {
        onSaved(createdID);
        onOpenChange(false);
      });
  };

  const clearPreview = () => {
    previewMutation.reset();
    updateMutation.reset();
  };
  const updateRow = (id: string, next: TunnelMappingInput) => {
    clearPreview();
    setRows((current) => current.map((row) => (row.id === id ? { ...row, value: next } : row)));
  };

  const addRow = (make: (existing: TunnelMappingInput[]) => TunnelMappingInput) => {
    clearPreview();
    setRows((current) => [...current, ...toRows([make(current.map((row) => row.value))])]);
  };

  const saving = operation.isPending || previewMutation.isPending || updateMutation.isPending;

  useAutomationForm(
    isEdit ? "tunnel.edit" : "tunnel.create",
    open,
    tunnel?.id ?? serverId,
    { name, serverId, frpsBindPort: String(frpsBindPort) },
    {
      name: setName,
      serverId: (value) => {
        if (!serverOptions.some((row) => row.value === value && !row.disabled))
          throw Error("AUTOMATION_OBJECT_UNAVAILABLE");
        setServerId(value);
      },
      frpsBindPort: (value) => {
        const port = Number(value);
        if (!Number.isInteger(port) || port < 1 || port > 65535)
          throw Error("AUTOMATION_FIELD_INVALID");
        setFrpsBindPort(port);
      },
    },
    () => onOpenChange(false),
    async () => {
      if (!canSubmit || saving) throw Error("AUTOMATION_FORM_INVALID");
      await save();
    },
  );
  return (
    <InspectorDrawer
      description={isEdit ? t("tunnels.dialogEditHint") : t("tunnels.dialogCreateHint")}
      footer={
        <>
          <IqujiButton
            data-ops-guide-command="close"
            variant="secondary"
            disabled={saving}
            onClick={() => onOpenChange(false)}
          >
            {t("common.cancel")}
          </IqujiButton>
          <IqujiButton
            disabled={saving || !serverEligible}
            onClick={() => void previewMutation.mutate(draft)}
            variant="secondary"
          >
            {t("tunnels.btnPreflight")}
          </IqujiButton>
          <IqujiButton
            data-ops-guide-command="submit"
            disabled={!canSubmit || saving}
            loading={saving}
            onClick={() => void save().catch(() => refreshServerStatus(serverId))}
          >
            {isEdit ? t("tunnels.btnSave") : t("tunnels.btnSaveAndStart")}
          </IqujiButton>
        </>
      }
      onClose={() => {
        if (operation.isPending || updateMutation.isPending) return;
        onOpenChange(false);
      }}
      open={open}
      size="xl"
      title={
        isEdit ? t("tunnels.dialogEditNamed", { name: tunnel?.name ?? "" }) : t("tunnels.newTunnel")
      }
    >
      <div
        data-ops-guide-form={tunnel ? "tunnel.edit" : "tunnel.create"}
        className="ops-form-stack"
      >
        {open && <ActionGuideCard form={tunnel ? "tunnel.edit" : "tunnel.create"} />}
        <div className="ops-form-grid">
          <IqujiField label={t("tunnels.fieldTunnelName")} required>
            <IqujiInput
              onChange={(event) => {
                clearPreview();
                setName(event.currentTarget.value);
              }}
              placeholder={t("tunnels.fieldTunnelPlaceholder")}
              data-ops-guide-field="name"
              value={name}
            />
          </IqujiField>
          <FieldWithHelp
            help={
              servers.length > 0 && (!firstAvailableServerId || selectedServer?.disabled)
                ? t("tunnels.serverFrpsCheckHint")
                : undefined
            }
            label={t("tunnels.fieldServer")}
            required
          >
            <IqujiSelect
              disabled={saving}
              onValueChange={(value) => {
                if (!serverOptions.some((option) => option.value === value && !option.disabled))
                  return;
                clearPreview();
                setServerId(value ?? "");
              }}
              options={serverOptions}
              placeholder={t("tunnels.selectReadyServer")}
              data-ops-guide-field="serverId"
              value={serverEligible || keepsBinding ? serverId : null}
            />
          </FieldWithHelp>
          <IqujiField label={t("tunnels.fieldFrpsPort")}>
            <IqujiInput
              min={1024}
              onChange={(event) => {
                clearPreview();
                setFrpsBindPort(Number(event.currentTarget.value) || 0);
              }}
              type="number"
              data-ops-guide-field="frpsBindPort"
              value={frpsBindPort}
            />
          </IqujiField>
        </div>

        <section className="ops-form-section">
          <div className="ops-tunnel-mapping-form__head">
            <h2 className="ops-form-section__title">
              {t("tunnels.sectionMappings", { count: mappings.length })}
            </h2>
            <div className="ops-tunnel-mapping__actions">
              <IqujiButton
                compact
                disabled={mappings.length >= MAPPING_LIMIT}
                onClick={() => addRow(createDefaultMapping)}
                variant="secondary"
              >
                <IqujiIcon decorative name="plus" />
                {t("tunnels.typeLocalService")}
              </IqujiButton>
              <IqujiButton
                compact
                disabled={mappings.length >= MAPPING_LIMIT}
                onClick={() => addRow(createProbeMapping)}
                variant="secondary"
              >
                <IqujiIcon decorative name="radar" />
                {t("tunnels.typeHttpProbe")}
              </IqujiButton>
            </div>
          </div>
          {domains.isError ? (
            <IqujiAlert title={t("tunnels.domainAssetsLoadFailed")} tone="danger">
              {errorMessage(domains.error)}
            </IqujiAlert>
          ) : null}
          {rows.map((row, index) => (
            <TunnelMappingFields
              domainNames={domainNames}
              domainsLoading={domains.isPending}
              index={index}
              key={row.id}
              mapping={row.value}
              onChange={(next) => updateRow(row.id, next)}
              onEntrypointChange={(next) => updateRow(row.id, applyEntrypoint(row.value, next))}
              onRemove={
                rows.length > 1
                  ? () => {
                      clearPreview();
                      setRows((current) => current.filter((item) => item.id !== row.id));
                    }
                  : undefined
              }
            />
          ))}
        </section>

        {preview ? (
          <div className="ops-tunnel-preflight">
            {capabilityChecks.length > 0 ? (
              <ul className="ops-tunnel-preflight" aria-label={t("tunnels.preflightChecks")}>
                {capabilityChecks.map((check) => (
                  <li key={check.id}>
                    {PREFLIGHT_CAPABILITIES[check.details?.capability ?? ""]}{" "}
                    <IqujiBadge tone={check.outcome === "passed" ? "success" : "danger"}>
                      {t(
                        check.outcome === "passed"
                          ? "tunnels.preflightCheckPassed"
                          : "tunnels.preflightCheckBlocked",
                      )}
                    </IqujiBadge>
                  </li>
                ))}
              </ul>
            ) : null}
            {preview.tunnelPreview?.endpoints?.length
              ? preview.tunnelPreview.endpoints.map((endpoint) => (
                  <code className="ops-tunnel-preflight__endpoint" key={endpoint}>
                    {endpoint}
                  </code>
                ))
              : null}
            {blockedByPreview ? (
              <IqujiAlert title={t("tunnels.preflightFailed")} tone="danger">
                <ul className="ops-tunnel-preflight">
                  {(preview.process?.decision?.blockers ?? []).map((code) => (
                    <li key={code}>{tunnelBlockReasonLabel(code)}</li>
                  ))}
                </ul>
              </IqujiAlert>
            ) : (
              <IqujiAlert title={t("tunnels.preflightPassed")} tone="success">
                {t("tunnels.preflightPassedDesc")}
              </IqujiAlert>
            )}
          </div>
        ) : null}

        {previewMutation.isError ? (
          <IqujiAlert title={t("tunnels.preflightError")} tone="danger">
            {errorMessage(previewMutation.error)}
          </IqujiAlert>
        ) : null}
        {updateMutation.isError ? (
          <IqujiAlert title={t("tunnels.saveFailed")} tone="danger">
            {errorMessage(updateMutation.error)}
          </IqujiAlert>
        ) : null}
      </div>
    </InspectorDrawer>
  );
}

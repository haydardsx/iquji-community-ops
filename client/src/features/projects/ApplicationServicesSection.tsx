import {
  IqujiAlert,
  IqujiButton,
  IqujiEmptyState,
  IqujiMultiSelect,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Controller, useFieldArray } from "react-hook-form";
import type { Server } from "@/contracts/types";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { SectionPanel } from "@/shared/ui/section-panel";
import { type ConfigFormProps, FormSelect, FormText } from "./ApplicationFields";
import { fetchDeploymentEligibility } from "./api";
import { deploymentServerOptions, managerOptions } from "./deployment-eligibility";
import "./ApplicationRuntimeSection.css";
import "./ApplicationSourceSection.css";

export function ApplicationServicesSection({
  form,
  disabled,
  servers,
  applicationId,
}: ConfigFormProps & { servers: Server[]; applicationId: string }) {
  const { t } = useI18n();
  const rows = useFieldArray({ control: form.control, name: "services" });
  const scope = form.watch("eligibleServerIds") ?? [];
  const services = form.watch("services") ?? [];
  const deploymentManagerServerId = form.watch("deploymentManagerServerId");
  const currentOverlay = form.watch("overlayNetwork");
  const [advanced, setAdvanced] = useState(false);
  const eligibility = useQuery({
    queryKey: ["deployment-eligibility", applicationId, deploymentManagerServerId],
    queryFn: () => fetchDeploymentEligibility(applicationId, deploymentManagerServerId),
    enabled: Boolean(deploymentManagerServerId),
  });
  const facts = eligibility.isError ? undefined : eligibility.data;
  const options = deploymentServerOptions(servers, facts, scope, undefined, t);
  const addSlot = () => {
    const base = applicationId.toLowerCase().replaceAll("_", "-");
    const keys = new Set(services.map((s) => s.slotKey));
    const names = new Set(services.map((s) => s.serviceName));
    let index = 1;
    while (keys.has(`service-${index}`) || names.has(`${base}-${index}`)) index++;
    rows.append({
      slotKey: `service-${index}`,
      serviceName: `${base}-${index}`,
      serverId: "",
      replicas: 1,
    });
  };
  return (
    <div className="ops-runtime-layout">
      <SectionPanel icon="server" title={t("projects.placementTitle")}>
        <div className="ops-runtime-fields">
          <FormSelect
            form={form}
            disabled={disabled}
            name="deploymentManagerServerId"
            label={t("projects.deploymentManager")}
            options={managerOptions(servers, t)}
          />
          <Controller
            control={form.control}
            name="eligibleServerIds"
            render={({ field }) => (
              <IqujiMultiSelect
                label={t("projects.allowedDeployServers")}
                disabled={disabled}
                clearLabel={t("projects.serverNone")}
                options={options.map((o) => ({
                  ...o,
                  disabled: o.disabled && !field.value.includes(o.value),
                }))}
                value={field.value}
                onChange={field.onChange}
                placeholder={t("projects.serverNone")}
              />
            )}
          />
        </div>
        <div className="ops-advanced-disclosure">
          <div className="ops-advanced-disclosure__bar">
            <IqujiButton
              variant="ghost"
              compact
              className="ops-advanced-disclosure__trigger"
              iconStart={
                <IqujiIcon
                  decorative
                  name={advanced ? "chevron-down" : "chevron-right"}
                  size={14}
                />
              }
              aria-expanded={advanced}
              aria-controls="application-placement-advanced"
              onClick={() => setAdvanced(!advanced)}
            >
              {advanced ? t("projects.advancedCollapse") : t("projects.advanced")}
            </IqujiButton>
            {!advanced && currentOverlay ? (
              <span className="ops-advanced-disclosure__hint">
                {t("projects.overlayNetwork")}: {currentOverlay}
              </span>
            ) : null}
          </div>
          {advanced ? (
            <div id="application-placement-advanced" className="ops-advanced-disclosure__body">
              <FormText
                form={form}
                disabled={disabled}
                name="overlayNetwork"
                label={t("projects.overlayNetwork")}
                placeholder={t("projects.overlayNetworkPlaceholder")}
              />
            </div>
          ) : null}
        </div>
        {eligibility.isError ? (
          <IqujiAlert title={t("projects.checkDeploymentEligibilityFailed")} tone="danger">
            {errorMessage(eligibility.error)}{" "}
            <IqujiButton compact variant="text" onClick={() => void eligibility.refetch()}>
              {t("projects.recheck")}
            </IqujiButton>
          </IqujiAlert>
        ) : null}
      </SectionPanel>
      <SectionPanel
        icon="boxes"
        title={t("projects.configuredSlots")}
        actions={
          <IqujiButton compact variant="secondary" disabled={disabled} onClick={addSlot}>
            {t("projects.addService")}
          </IqujiButton>
        }
      >
        {!rows.fields.length ? (
          <IqujiEmptyState
            title={t("projects.emptyServices")}
            body={t("projects.emptyServicesDesc")}
          />
        ) : null}
        <div className="ops-runtime-slots">
          {rows.fields.map((row, index) => (
            <section className="ops-runtime-slot" key={row.id}>
              <div className="ops-runtime-slot__header">
                <h3>
                  {services[index]?.serviceName || t("projects.serviceIndex", { index: index + 1 })}
                </h3>
                <IqujiButton
                  compact
                  variant="text"
                  disabled={disabled}
                  onClick={() => rows.remove(index)}
                >
                  {t("common.remove")}
                </IqujiButton>
              </div>
              <div className="ops-runtime-fields">
                {advanced && (
                  <FormText
                    form={form}
                    disabled={disabled}
                    name={`services.${index}.serviceName`}
                    label={t("projects.serviceName")}
                    required
                    placeholder={t("projects.serviceNamePlaceholder")}
                  />
                )}
                <FormSelect
                  form={form}
                  disabled={disabled}
                  name={`services.${index}.serverId`}
                  label={t("projects.serviceServer")}
                  options={[
                    { label: t("projects.serverNone"), value: "" },
                    ...deploymentServerOptions(
                      servers,
                      facts,
                      [services[index]?.serverId ?? ""],
                      scope,
                      t,
                    ),
                  ]}
                />
                {advanced && (
                  <FormText
                    form={form}
                    disabled={disabled}
                    name={`services.${index}.slotKey`}
                    label={t("projects.serviceSlotKey")}
                    help={t("projects.slotKeyDesc")}
                    required
                  />
                )}
                <FormText
                  form={form}
                  disabled={disabled}
                  name={`services.${index}.replicas`}
                  label={t("projects.serviceReplicas")}
                  type="number"
                  required
                />
              </div>
            </section>
          ))}
        </div>
      </SectionPanel>
    </div>
  );
}

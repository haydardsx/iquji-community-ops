import { IqujiField, IqujiInput, IqujiSelect } from "@iquji-community/web-component";
import { useFieldArray } from "react-hook-form";
import type { ApplicationOutput, RouteRow, SecretAsset } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import type { ConfigFormProps } from "./ApplicationFields";
import {
  ApplicationVariableTable,
  applicationCredentialOptions,
  RemoveVariableButton,
  variableReferenceOptions,
} from "./ApplicationVariableTable";

export function ApplicationOutputsSection({
  form,
  disabled,
  secrets,
  routes,
  gitSource,
}: ConfigFormProps & { secrets: SecretAsset[]; routes: RouteRow[]; gitSource: boolean }) {
  const { t } = useI18n();
  const rows = useFieldArray({ control: form.control, name: "outputs", keyName: "rowId" });
  const values = form.watch("outputs");
  const variables = form.watch("variables");
  const services = form.watch("services");
  const ports = form.watch("ports");
  const kinds: { value: ApplicationOutput["valueSource"]; label: string }[] = [
    { value: "plain_text", label: t("apps.varLiteral") },
    { value: "variable", label: t("apps.varInputRef") },
    { value: "secret_ref", label: t("apps.varCredential") },
    { value: "service_host", label: t("apps.varServiceHost") },
    { value: "service_port", label: t("apps.varServicePort") },
    { value: "route_url", label: t("apps.varRoute") },
    { value: "static_url", label: t("apps.varStaticSite") },
    { value: "build_output", label: t("apps.varBuildResult") },
  ];
  const inputOptions = variables
    .filter((v) => v.key)
    .map((v) => ({
      value: JSON.stringify([v.stage, v.serviceSlotKey ?? "", v.key]),
      label: [v.key, t(v.stage === "build" ? "apps.varBuild" : "apps.varRuntime"), v.serviceSlotKey]
        .filter(Boolean)
        .join(" · "),
    }));
  const serviceOptions = services.map((s) => ({
    value: s.slotKey,
    label: s.serviceName || s.slotKey,
  }));
  const portOptions = ports.map((p) => ({ value: p.name, label: `${p.name} · ${p.targetPort}` }));
  const routeOptions = routes.map((r) => ({
    value: r.id,
    label: r.accessibleUrls?.[0] ?? r.domains?.[0]?.name ?? r.id,
  }));
  const credentialOptions = applicationCredentialOptions(secrets);
  const selectProps = { compact: true, disabled, placeholder: t("apps.varSelect") };
  return (
    <ApplicationVariableTable
      title={t("apps.varOutputs")}
      actionLabel={t("apps.varAddOutput")}
      count={rows.fields.length}
      disabled={disabled}
      onAdd={() =>
        rows.append({
          id: crypto.randomUUID(),
          key: "",
          visibility: "project",
          valueSource: "plain_text",
          plainValue: "",
        })
      }
      columns={[
        t("apps.varName"),
        t("apps.varSource"),
        t("apps.varVisibility"),
        t("common.value"),
        t("common.actions"),
      ]}
      emptyTitle={t("apps.varNoOutputs")}
      description={t("apps.varOutputHelp")}
    >
      {rows.fields.map((row, i) => {
        const o = values[i] ?? row;
        const write = (value: ApplicationOutput) =>
          form.setValue(`outputs.${i}`, value, {
            shouldDirty: true,
            shouldValidate: form.formState.isSubmitted,
          });
        const update = (next: Partial<ApplicationOutput>) => write({ ...o, ...next });
        const errors = form.formState.errors.outputs?.[i];
        const inputRef = o.variableKey
          ? JSON.stringify([o.variableStage ?? "runtime", o.serviceSlotKey ?? "", o.variableKey])
          : undefined;
        return (
          <div key={row.rowId} className="ops-variable-row-wrapper">
            <div className="ops-variable-row">
              <IqujiField label={t("apps.varName")} error={errors?.key?.message}>
                <IqujiInput
                  disabled={disabled}
                  placeholder="API_URL"
                  {...form.register(`outputs.${i}.key`)}
                />
              </IqujiField>
              <IqujiField label={t("apps.varSource")}>
                <IqujiSelect
                  {...selectProps}
                  value={o.valueSource}
                  options={kinds.filter((k) => gitSource || k.value !== "build_output")}
                  onValueChange={(v) => {
                    if (!v || v === o.valueSource) return;
                    write({
                      id: o.id,
                      key: o.key,
                      visibility: o.visibility,
                      valueSource: v,
                      ...(v === "plain_text" ? { plainValue: "" } : {}),
                    });
                  }}
                />
              </IqujiField>
              <IqujiField label={t("apps.varVisibility")}>
                <IqujiSelect
                  {...selectProps}
                  value={o.visibility}
                  options={[
                    { value: "project", label: t("apps.varProject") },
                    { value: "cross_project", label: t("apps.varCrossProject") },
                  ]}
                  onValueChange={(v) =>
                    update({ visibility: v === "cross_project" ? v : "project" })
                  }
                />
              </IqujiField>
              <div
                className={`ops-variable-cell ${
                  o.valueSource === "service_port" || o.valueSource === "service_host"
                    ? "ops-variable-cell--compound"
                    : ""
                }`}
              >
                {o.valueSource === "plain_text" && (
                  <IqujiField label={t("common.value")}>
                    <IqujiInput disabled={disabled} {...form.register(`outputs.${i}.plainValue`)} />
                  </IqujiField>
                )}
                {o.valueSource === "variable" && (
                  <IqujiField label={t("apps.varInputRef")}>
                    <IqujiSelect
                      {...selectProps}
                      value={inputRef ?? null}
                      options={variableReferenceOptions(
                        inputOptions,
                        inputRef,
                        t("apps.varUnavailable"),
                      )}
                      onValueChange={(s) => {
                        if (!s) return;
                        const [variableStage, slot, variableKey] = JSON.parse(s);
                        update({ variableStage, serviceSlotKey: slot || undefined, variableKey });
                      }}
                    />
                  </IqujiField>
                )}
                {o.valueSource === "secret_ref" && (
                  <IqujiField label={t("apps.varCredential")}>
                    <IqujiSelect
                      {...selectProps}
                      value={o.secretRef || null}
                      options={variableReferenceOptions(
                        credentialOptions,
                        o.secretRef,
                        t("apps.varUnavailable"),
                      )}
                      onValueChange={(s) => update({ secretRef: s ?? "" })}
                    />
                  </IqujiField>
                )}
                {(o.valueSource === "service_port" || o.valueSource === "service_host") && (
                  <>
                    <IqujiField label={t("apps.varService")}>
                      <IqujiSelect
                        {...selectProps}
                        value={o.serviceSlotKey || null}
                        options={variableReferenceOptions(
                          serviceOptions,
                          o.serviceSlotKey,
                          t("apps.varUnavailable"),
                        )}
                        onValueChange={(s) => update({ serviceSlotKey: s ?? "" })}
                      />
                    </IqujiField>
                    <IqujiField label={t("apps.varEndpoint")}>
                      <IqujiSelect
                        {...selectProps}
                        value={o.endpointMode ?? "internal"}
                        options={[
                          { value: "internal", label: t("apps.varInternal") },
                          { value: "published", label: t("apps.varPublished") },
                        ]}
                        onValueChange={(s) =>
                          update({ endpointMode: s === "published" ? "published" : "internal" })
                        }
                      />
                    </IqujiField>
                  </>
                )}
                {(o.valueSource === "service_port" ||
                  (o.valueSource === "service_host" && o.endpointMode === "published")) && (
                  <IqujiField label={t("apps.varPortName")}>
                    <IqujiSelect
                      {...selectProps}
                      value={o.portName || null}
                      options={variableReferenceOptions(
                        portOptions,
                        o.portName,
                        t("apps.varUnavailable"),
                      )}
                      onValueChange={(s) => update({ portName: s ?? "" })}
                    />
                  </IqujiField>
                )}
                {(o.valueSource === "route_url" || o.valueSource === "static_url") && (
                  <IqujiField label={t("apps.varInlet")}>
                    <IqujiSelect
                      {...selectProps}
                      value={o.routeId || null}
                      options={variableReferenceOptions(
                        routeOptions,
                        o.routeId,
                        t("apps.varUnavailable"),
                      )}
                      onValueChange={(s) => update({ routeId: s ?? "" })}
                    />
                  </IqujiField>
                )}
                {o.valueSource === "build_output" && (
                  <span className="ops-muted">{t("apps.varBuildResultHelp")}</span>
                )}
              </div>
              <RemoveVariableButton
                disabled={disabled}
                label={t("apps.varRemoveOutput")}
                onClick={() => rows.remove(i)}
              />
            </div>
          </div>
        );
      })}
    </ApplicationVariableTable>
  );
}

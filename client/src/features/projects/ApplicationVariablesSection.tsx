import { IqujiField, IqujiInput, IqujiSelect } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useFieldArray } from "react-hook-form";
import type { AvailableOutput, SecretAsset } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { FieldHelp, FieldWithHelp } from "@/shared/ui/field-help";
import type { ConfigFormProps } from "./ApplicationFields";
import {
  ApplicationVariableTable,
  applicationCredentialOptions,
  RemoveVariableButton,
  variableReferenceOptions,
} from "./ApplicationVariableTable";

export function ApplicationVariablesSection({
  form,
  disabled,
  secrets,
  outputs = [],
}: ConfigFormProps & { secrets: SecretAsset[]; outputs?: AvailableOutput[] }) {
  const { t } = useI18n();
  const rows = useFieldArray({ control: form.control, name: "variables", keyName: "rowId" });
  const values = form.watch("variables");
  const services = form.watch("services");
  const provider = form.watch("source.provider");
  const sources = [
    { value: "plain_text", label: t("apps.varLiteral"), help: t("apps.varLiteralHelp") },
    { value: "secret_ref", label: t("apps.varCredential"), help: t("apps.varCredentialHelp") },
    { value: "output_ref", label: t("apps.varOutputRef"), help: t("apps.varOutputRefHelp") },
  ] as const;
  const stages = [
    { value: "runtime", label: t("apps.varRuntime") },
    ...(provider === "image" ? [] : [{ value: "build", label: t("apps.varBuild") }]),
  ];
  const scopeOptions = [
    { value: "", label: t("apps.varAllServices") },
    ...services.map((s) => ({ value: s.slotKey, label: s.serviceName || s.slotKey })),
  ];
  const credentialOptions = applicationCredentialOptions(secrets);
  const outputOptions = outputs.map((o) => ({
    value: o.id,
    label: [o.applicationName, o.key, o.sensitive ? t("apps.varSensitive") : undefined]
      .filter(Boolean)
      .join(" · "),
  }));
  return (
    <ApplicationVariableTable
      title={t("apps.varInputs")}
      actionLabel={t("apps.varAddInput")}
      count={rows.fields.length}
      disabled={disabled}
      onAdd={() =>
        rows.append({
          key: "",
          valueSource: "plain_text",
          stage: "runtime",
          scope: "global",
          plainValue: "",
        })
      }
      columns={[
        t("apps.varName"),
        t("apps.varSource"),
        t("apps.varScope"),
        t("apps.varStage"),
        t("common.value"),
        t("common.actions"),
      ]}
      emptyTitle={t("apps.varNoInputs")}
      description={t("apps.varInputHelp")}
      guide={
        <section className="ops-variable-guide" aria-label={t("apps.varGuide")}>
          <div className="ops-variable-guide__title">
            <IqujiIcon decorative name="circle-info" size={14} />
            <span>{t("apps.varGuide")}</span>
          </div>
          <div className="ops-variable-guide__items">
            {sources.map((source) => (
              <div key={source.value} className="ops-variable-guide__item">
                <strong>{source.label}</strong>
                <span>{source.help}</span>
              </div>
            ))}
          </div>
        </section>
      }
    >
      {rows.fields.map((row, i) => {
        const v = values[i] ?? row;
        const update = (fields: Partial<typeof v>) =>
          form.setValue(
            `variables.${i}`,
            { ...v, ...fields },
            { shouldDirty: true, shouldValidate: form.formState.isSubmitted },
          );
        const buildSecret =
          v.stage === "build" &&
          (v.valueSource === "secret_ref" ||
            (v.valueSource === "output_ref" &&
              outputs.some((o) => o.id === v.plainValue && o.sensitive)));
        const errors = form.formState.errors.variables?.[i];
        return (
          <div key={row.rowId} className="ops-variable-row-wrapper">
            <div className="ops-variable-row">
              <FieldWithHelp
                help={v.metadata?.description}
                label={t("apps.varName")}
                error={errors?.key?.message}
              >
                <IqujiInput
                  className="ops-variable-key-input"
                  disabled={disabled}
                  placeholder="DATABASE_HOST"
                  {...form.register(`variables.${i}.key`)}
                />
              </FieldWithHelp>
              <IqujiField label={t("apps.varSource")}>
                <IqujiSelect
                  compact
                  disabled={disabled}
                  value={v.valueSource}
                  options={sources}
                  onValueChange={(s) => {
                    if (s && s !== v.valueSource)
                      update({
                        valueSource: s,
                        plainValue: "",
                        outputApplicationId: undefined,
                      });
                  }}
                />
              </IqujiField>
              <IqujiField label={t("apps.varScope")}>
                <IqujiSelect
                  compact
                  disabled={disabled}
                  value={v.scope === "global" ? "" : v.serviceSlotKey}
                  options={variableReferenceOptions(
                    scopeOptions,
                    v.serviceSlotKey,
                    t("apps.varUnavailable"),
                  )}
                  onValueChange={(s) =>
                    update({
                      scope: s ? "service" : "global",
                      serviceSlotKey: s || undefined,
                    })
                  }
                />
              </IqujiField>
              <IqujiField label={t("apps.varStage")}>
                <div className="ops-variable-stage-field">
                  <IqujiSelect
                    compact
                    disabled={disabled}
                    value={v.stage}
                    options={stages}
                    onValueChange={(s) => update({ stage: s === "build" ? "build" : "runtime" })}
                  />
                  {buildSecret ? (
                    <FieldHelp label={t("apps.varStage")} help={t("apps.varBuildSecretHelp")} />
                  ) : null}
                </div>
              </IqujiField>
              <IqujiField label={t("common.value")} error={errors?.plainValue?.message}>
                {v.valueSource === "plain_text" ? (
                  <IqujiInput
                    className="ops-variable-value-input"
                    disabled={disabled}
                    {...form.register(`variables.${i}.plainValue`)}
                  />
                ) : (
                  <IqujiSelect
                    compact
                    disabled={disabled}
                    value={v.plainValue || null}
                    placeholder={t("apps.varSelect")}
                    options={variableReferenceOptions(
                      v.valueSource === "secret_ref" ? credentialOptions : outputOptions,
                      v.plainValue,
                      t("apps.varUnavailable"),
                    )}
                    onValueChange={(s) =>
                      update({
                        plainValue: s ?? "",
                        outputApplicationId:
                          v.valueSource === "output_ref"
                            ? outputs.find((o) => o.id === s)?.applicationId
                            : undefined,
                      })
                    }
                  />
                )}
              </IqujiField>
              <RemoveVariableButton
                disabled={disabled}
                label={t("apps.varRemoveInput")}
                onClick={() => rows.remove(i)}
              />
            </div>
          </div>
        );
      })}
    </ApplicationVariableTable>
  );
}

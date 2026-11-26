import type { SecretAsset } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { ApplicationConfigPanel } from "./ApplicationConfigPanel";
import { type ConfigFormProps, FormSelect, FormText } from "./ApplicationFields";
import { applicationCredentialOptions, variableReferenceOptions } from "./ApplicationVariableTable";

export function RegistrySyncSection({
  form,
  disabled,
  secrets,
}: ConfigFormProps & { secrets: SecretAsset[] }) {
  const { t } = useI18n();
  return (
    <ApplicationConfigPanel
      icon="container"
      title={t("projects.registrySync")}
      description={t("projects.registrySyncHint")}
    >
      <FormSelect
        form={form}
        disabled={disabled}
        name="registry.mode"
        label={t("projects.registrySync")}
        options={[
          { value: "disabled", label: t("projects.registryOff") },
          { value: "enabled", label: t("projects.registryOn") },
        ]}
      />
      {form.watch("registry.mode") === "enabled" && (
        <>
          <FormText
            form={form}
            disabled={disabled}
            name="registry.repository"
            label={t("projects.registryRepository")}
            placeholder="registry.example.org/team/service"
            required
          />
          <div className="ops-source-grid--2">
            <FormText
              form={form}
              disabled={disabled}
              name="registry.username"
              label={t("projects.registryUsername")}
            />
            <FormSelect
              form={form}
              disabled={disabled}
              name="registry.secretRef"
              label={t("projects.registryCredential")}
              options={variableReferenceOptions(
                [
                  { value: "", label: t("projects.registryAnonymous") },
                  ...applicationCredentialOptions(secrets, "registry_secret"),
                ],
                form.watch("registry.secretRef"),
                t("projects.credentialUnavailable"),
              )}
            />
          </div>
        </>
      )}
    </ApplicationConfigPanel>
  );
}

import { IqujiButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useEffect } from "react";
import { type UseFieldArrayReturn, useFieldArray } from "react-hook-form";
import { useI18n } from "@/shared/i18n";
import { SectionPanel } from "@/shared/ui/section-panel";
import { ApplicationCommandField } from "./ApplicationCommandField";
import { type ConfigFormProps, FormSelect, FormText } from "./ApplicationFields";
import type { ApplicationForm } from "./model";
import { nextVolumeDefaults } from "./schemas";

export function ContainerFields({
  form,
  disabled,
  ports,
}: ConfigFormProps & { ports: UseFieldArrayReturn<ApplicationForm, "ports"> }) {
  const { t } = useI18n();
  const volumes = useFieldArray({ control: form.control, name: "volumes" });
  const published = form.watch("publishedPort");
  const legacyPort = form.watch("containerPort");
  const services = form.watch("services");
  const legacyMappings = new Set(
    services.map((service) => service.publishedPort || published || 0),
  );
  const conflictingLegacyMappings = !ports.fields.length && legacyMappings.size > 1;
  const addPort = () => {
    if (ports.fields.length) {
      ports.append({ name: "", protocol: "http", targetPort: 80 });
      return;
    }
    const publishedPort = services.length ? [...legacyMappings][0] : published;
    ports.append({
      name: "web",
      protocol: "http",
      targetPort: legacyPort ?? 80,
      publishedPort: publishedPort || undefined,
    });
  };
  useEffect(() => {
    const subscription = form.watch((_values, { name }) => {
      if (name === "volumes" || name?.startsWith("volumes.")) void form.trigger("volumes");
    });
    return () => subscription.unsubscribe();
  }, [form]);
  return (
    <div className="ops-form-stack">
      <SectionPanel
        icon="network"
        title={t("projects.ports")}
        description={t("projects.portsDesc")}
        actions={
          <IqujiButton
            compact
            disabled={disabled || conflictingLegacyMappings}
            variant="secondary"
            iconStart={<IqujiIcon decorative name="plus" size={14} />}
            onClick={addPort}
          >
            {t("projects.addPort")}
          </IqujiButton>
        }
      >
        <div className="ops-container-fields-body">
          {!ports.fields.length && legacyPort !== undefined && (
            <FormText
              form={form}
              disabled={disabled}
              name="containerPort"
              label={t("projects.containerPort")}
              type="number"
              required
            />
          )}
          {ports.fields.map((row, i) => (
            <div className="ops-container-item-row ops-container-port-row" key={row.id}>
              <FormText
                form={form}
                disabled={disabled}
                name={`ports.${i}.name`}
                label={t("common.name")}
              />
              <FormSelect
                form={form}
                disabled={disabled}
                name={`ports.${i}.protocol`}
                label={t("projects.protocol")}
                options={["http", "tcp", "udp"].map((value) => ({
                  value,
                  label: value.toUpperCase(),
                }))}
              />
              <FormText
                form={form}
                disabled={disabled}
                name={`ports.${i}.targetPort`}
                type="number"
                label={t("projects.containerPort")}
              />
              <div className="ops-container-item-row__action">
                <IqujiButton
                  compact
                  variant="ghost"
                  disabled={disabled}
                  aria-label={t("common.remove")}
                  title={t("common.remove")}
                  onClick={() => {
                    ports.remove(i);
                    if (ports.fields.length === 1) {
                      form.setValue("containerPort", undefined, { shouldDirty: true });
                      form.setValue("publishedPort", undefined, { shouldDirty: true });
                      for (const [index] of services.entries())
                        form.setValue(`services.${index}.publishedPort`, undefined, {
                          shouldDirty: true,
                        });
                    }
                  }}
                  className="ops-container-item-remove"
                >
                  <IqujiIcon decorative name="trash" size={14} />
                </IqujiButton>
              </div>
            </div>
          ))}
          {conflictingLegacyMappings && (
            <p className="ops-muted">{t("projects.clearLegacyMappings")}</p>
          )}
        </div>
      </SectionPanel>
      <SectionPanel
        icon="hard-drive"
        title={t("projects.volumes")}
        description={t("projects.volumesDesc")}
        actions={
          <IqujiButton
            compact
            disabled={disabled}
            variant="secondary"
            iconStart={<IqujiIcon decorative name="plus" size={14} />}
            onClick={() => volumes.append(nextVolumeDefaults(form.getValues("volumes") ?? []))}
          >
            {t("projects.addVolume")}
          </IqujiButton>
        }
      >
        <div className="ops-container-fields-body">
          {volumes.fields.map((row, i) => (
            <div className="ops-container-item-row ops-container-volume-row" key={row.id}>
              <FormText
                form={form}
                disabled={disabled}
                name={`volumes.${i}.name`}
                label={t("projects.volumeName")}
                placeholder="data"
                help={t("projects.volumeNameHelp")}
                required
              />
              <FormText
                form={form}
                disabled={disabled}
                name={`volumes.${i}.target`}
                label={t("projects.mountTarget")}
                placeholder="/data"
                help={t("projects.volumeTargetHelp")}
                required
              />
              <div className="ops-container-item-row__action">
                <IqujiButton
                  compact
                  variant="ghost"
                  disabled={disabled}
                  aria-label={t("common.remove")}
                  title={t("common.remove")}
                  onClick={() => volumes.remove(i)}
                  className="ops-container-item-remove"
                >
                  <IqujiIcon decorative name="trash" size={14} />
                </IqujiButton>
              </div>
            </div>
          ))}
        </div>
      </SectionPanel>
      {(["command", "healthCommand", "logCommand", "initializeCommand"] as const).map((name) => (
        <ApplicationCommandField
          key={name}
          name={name}
          label={t(`projects.${name}`)}
          form={form}
          disabled={disabled}
        />
      ))}
    </div>
  );
}

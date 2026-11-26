import { IqujiButton, IqujiField, IqujiInput, IqujiSelect } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useI18n } from "@/shared/i18n";
import { FieldWithHelp } from "@/shared/ui/field-help";
import { PROBE_DEFAULT_PORT } from "./mappings";
import type { TunnelMappingInput } from "./types";

const LOCAL_HOSTS = [
  { label: "127.0.0.1", value: "127.0.0.1" },
  { label: "localhost", value: "localhost" },
  { label: "::1", value: "::1" },
];

const ENTRYPOINTS = [
  { label: "HTTP · 80", value: "web" },
  { label: "HTTPS · 443", value: "websecure" },
];

/** 单条映射的字段组合；探针映射的本地地址由服务端填充，不再让用户填写。 */
export function TunnelMappingFields({
  domainNames,
  domainsLoading,
  index,
  mapping,
  onChange,
  onEntrypointChange,
  onRemove,
}: {
  domainNames: string[];
  domainsLoading: boolean;
  index: number;
  mapping: TunnelMappingInput;
  onChange: (next: TunnelMappingInput) => void;
  onEntrypointChange: (entrypoint: string) => void;
  onRemove?: () => void;
}) {
  const { t } = useI18n();
  const probe = mapping.targetType === "http_probe";
  const hasDomain = mapping.publicDomain !== "";
  const domainIsUnknown = hasDomain && !domainNames.includes(mapping.publicDomain);
  const needsDomain = !hasDomain && mapping.publicScheme === "https";
  const patch = (next: Partial<TunnelMappingInput>) => onChange({ ...mapping, ...next });

  return (
    <div className="ops-tunnel-mapping-form">
      <div className="ops-tunnel-mapping-form__head">
        <span className="ops-tunnel-mapping-form__index">
          {t("tunnels.mappingIndex", { index: index + 1 })}
        </span>
        <div className="ops-tunnel-mapping__actions">
          <IqujiSelect
            aria-label={t("tunnels.mappingTypeAria", { index: index + 1 })}
            compact
            onValueChange={(value) =>
              patch({
                localHost: "127.0.0.1",
                localPort: value === "http_probe" ? PROBE_DEFAULT_PORT : 3000,
                targetType: value === "http_probe" ? "http_probe" : "local_service",
              })
            }
            options={[
              { label: t("tunnels.typeLocalService"), value: "local_service" },
              { label: t("tunnels.typeHttpProbe"), value: "http_probe" },
            ]}
            value={mapping.targetType}
          />
          {onRemove ? (
            <IqujiButton compact onClick={onRemove} variant="ghost">
              <IqujiIcon decorative name="trash" />
              {t("tunnels.removeMapping")}
            </IqujiButton>
          ) : null}
        </div>
      </div>

      <div className="ops-form-grid">
        <IqujiField label={t("tunnels.fieldName")} required>
          <IqujiInput
            onChange={(event) => patch({ name: event.currentTarget.value })}
            placeholder={t("tunnels.fieldNamePlaceholder")}
            value={mapping.name}
          />
        </IqujiField>
        {probe ? (
          <IqujiField label={t("tunnels.fieldLocalTarget")}>
            <IqujiInput readOnly value={t("tunnels.builtInProbe")} />
          </IqujiField>
        ) : (
          <>
            <IqujiField label={t("tunnels.fieldLocalHost")}>
              <IqujiSelect
                onValueChange={(value) => patch({ localHost: value ?? "127.0.0.1" })}
                options={LOCAL_HOSTS}
                value={mapping.localHost}
              />
            </IqujiField>
            <IqujiField label={t("tunnels.fieldLocalPort")} required>
              <IqujiInput
                min={1}
                onChange={(event) => patch({ localPort: Number(event.currentTarget.value) || 0 })}
                type="number"
                value={mapping.localPort}
              />
            </IqujiField>
          </>
        )}
        <IqujiField
          label={t(hasDomain ? "tunnels.fieldFrpsProxyPort" : "tunnels.fieldPublishedProxyPort")}
          required
        >
          <IqujiInput
            min={1024}
            onChange={(event) => patch({ frpsProxyPort: Number(event.currentTarget.value) || 0 })}
            type="number"
            value={mapping.frpsProxyPort}
          />
        </IqujiField>
        <IqujiField
          error={
            needsDomain
              ? t("tunnels.fieldPublicDomainRequired")
              : domainIsUnknown && !domainsLoading
                ? t("tunnels.fieldPublicDomainUnregistered")
                : undefined
          }
          label={t("tunnels.fieldPublicDomain")}
        >
          <IqujiSelect
            aria-busy={domainsLoading}
            onValueChange={(value) =>
              patch(
                value
                  ? { publicDomain: value }
                  : { entrypoint: "web", publicDomain: "", publicScheme: "http" },
              )
            }
            options={[
              {
                label: domainsLoading ? t("common.loading") : t("tunnels.fieldPublicDomainDirect"),
                value: "",
              },
              ...domainNames.map((domain) => ({ label: domain, value: domain })),
              ...(domainIsUnknown
                ? [{ disabled: true, label: mapping.publicDomain, value: mapping.publicDomain }]
                : []),
            ]}
            value={mapping.publicDomain}
          />
        </IqujiField>
        <FieldWithHelp
          help={hasDomain ? undefined : t("tunnels.fieldDirectPathHint")}
          label={t("tunnels.fieldPathPrefix")}
        >
          <IqujiInput
            onChange={(event) => patch({ publicPathPrefix: event.currentTarget.value })}
            placeholder="/"
            value={mapping.publicPathPrefix}
          />
        </FieldWithHelp>
        {hasDomain || needsDomain ? (
          <IqujiField label={t("tunnels.fieldPublicEntry")}>
            <IqujiSelect
              onValueChange={(value) => onEntrypointChange(value ?? "web")}
              options={hasDomain ? ENTRYPOINTS : [ENTRYPOINTS[0]]}
              value={mapping.entrypoint}
            />
          </IqujiField>
        ) : null}
      </div>
    </div>
  );
}

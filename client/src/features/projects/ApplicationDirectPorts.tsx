import {
  IqujiAlert,
  IqujiBadge,
  IqujiInput,
  IqujiLoadingState,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import type { ApplicationRuntime } from "@/contracts/types";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { RecordSnippet } from "@/shared/ui/record-snippet";
import { SectionPanel } from "@/shared/ui/section-panel";
import { type ConfigFormProps, FormText } from "./ApplicationFields";
import "./ApplicationDirectPorts.css";

export function ApplicationDirectPorts({
  runtime,
  loading,
  error,
  form,
  disabled,
  portRows,
}: ConfigFormProps & {
  runtime?: ApplicationRuntime;
  loading: boolean;
  error: unknown;
  portRows: readonly { id: string }[];
}) {
  const { t } = useI18n();
  const declarations = form.watch("ports");
  const services = form.watch("services");
  const defaultPort = form.watch("containerPort");
  const ports =
    runtime?.availability === "available"
      ? runtime.services.flatMap((service) =>
          (service.ports ?? [])
            .filter((port) => Boolean(port.publishedPort))
            .map((port) => ({ service, port })),
        )
      : [];

  return (
    <SectionPanel
      icon="link"
      title={t("projects.directPorts")}
      description={
        t("projects.directPortsDesc") || "配置容器端口与宿主机端口的直接映射关系（绕过入口网关）。"
      }
    >
      <div className="ops-direct-ports">
        <div className="ops-direct-ports-list">
          {!declarations.length && defaultPort !== undefined && (
            <>
              <FormText
                form={form}
                disabled={disabled}
                type="number"
                name="publishedPort"
                label={`HTTP · ${defaultPort} → ${t("projects.defaultPublishedPort")}`}
              />
              {services.map((service, index) => (
                <FormText
                  key={service.slotKey}
                  form={form}
                  disabled={disabled}
                  type="number"
                  name={`services.${index}.publishedPort`}
                  label={`${service.serviceName || service.slotKey} · ${t("projects.slotPublishedPort")}`}
                />
              ))}
            </>
          )}

          {portRows.map((row, index) => {
            const port = declarations[index];
            if (!port) return null;

            return (
              <div key={row.id} className="ops-direct-port-card">
                <div className="ops-direct-port-card__left">
                  <div className="ops-direct-port-card__identity">
                    <strong className="ops-direct-port-card__name">
                      {port.name || t("projects.unnamedPort")}
                    </strong>
                    <span className="ops-direct-port-card__protocol">
                      {port.protocol.toUpperCase()}
                    </span>
                  </div>
                  <div className="ops-direct-port-card__target">
                    <span>{t("projects.containerPort") || "容器端口"}:</span>
                    <code>{port.targetPort || "—"}</code>
                  </div>
                </div>

                <div className="ops-direct-port-card__arrow">
                  <IqujiIcon decorative name="arrow-right" size={14} />
                </div>

                <div className="ops-direct-port-card__right">
                  <label
                    className="ops-direct-port-card__input-label"
                    htmlFor={`port-pub-${index}`}
                  >
                    {t("projects.servicePublishedPort")}
                  </label>
                  <div className="ops-direct-port-card__input-wrap">
                    <IqujiInput
                      id={`port-pub-${index}`}
                      {...form.register(`ports.${index}.publishedPort`, {
                        setValueAs: (value) => (value === "" ? undefined : Number(value)),
                      })}
                      disabled={disabled}
                      type="number"
                      placeholder={t("projects.portPlaceholder") || "留空取消"}
                    />
                  </div>
                </div>
              </div>
            );
          })}
        </div>

        <div className="ops-direct-ports__hint">
          <IqujiIcon decorative name="circle-info" size={14} />
          <span>{t("projects.portRedeployHint")}</span>
        </div>

        {loading ? (
          <IqujiLoadingState label={t("projects.checkingRemoteSwarm")} />
        ) : error ? (
          <IqujiAlert title={t("projects.readRuntimeStateFailed")} tone="danger">
            {errorMessage(error)}
          </IqujiAlert>
        ) : !ports.length ? (
          <p className="ops-muted">
            {t(
              runtime?.availability === "unavailable"
                ? "projects.runtimeUnavailable"
                : "projects.noDirectPorts",
            )}
          </p>
        ) : (
          <div className="ops-direct-ports-status-section">
            <div className="ops-direct-ports-status-section__head">
              <IqujiIcon decorative name="network" size={14} />
              <span className="ops-direct-ports-status-section__title">
                {t("projects.actualRuntimeState")}
              </span>
            </div>
            <div className="ops-direct-ports-status-grid">
              {ports.map(({ service, port }) => {
                const host = port.publishedHost;
                const isVerified = Boolean(host && port.publishedVerified);
                const endpoint =
                  host && port.publishedVerified
                    ? `${host.includes(":") && !host.startsWith("[") ? `[${host}]` : host}:${port.publishedPort}`
                    : "";

                return (
                  <div
                    key={`${service.serviceSlotKey}-${port.name}`}
                    className="ops-direct-port-status-card"
                  >
                    <div className="ops-direct-port-status-card__header">
                      <div className="ops-direct-port-status-card__identity">
                        <strong className="ops-direct-port-status-card__name">{port.name}</strong>
                        <span className="ops-direct-port-status-card__protocol">
                          {port.protocol.toUpperCase()}
                        </span>
                        <RecordSnippet
                          value={service.serviceName}
                          label={service.serviceName}
                          truncateLength={16}
                        />
                      </div>
                      <IqujiBadge tone={isVerified ? "success" : "neutral"}>
                        {isVerified
                          ? t("state.verified") || "已验证"
                          : t("state.unverified") || "未验证"}
                      </IqujiBadge>
                    </div>
                    <div className="ops-direct-port-status-card__body">
                      {endpoint ? (
                        <div className="ops-direct-port-status-card__endpoint">
                          <IqujiIcon decorative name="globe" size={13} />
                          <RecordSnippet
                            value={endpoint}
                            label={t("projects.directEndpoint") || "直连地址"}
                            truncateLength={32}
                          />
                        </div>
                      ) : (
                        <span className="ops-muted">
                          {t("projects.unverifiedMapping", { port: port.publishedPort })}
                        </span>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </div>
    </SectionPanel>
  );
}

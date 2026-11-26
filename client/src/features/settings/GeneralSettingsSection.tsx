import {
  IconButton,
  IqujiBadge,
  IqujiButton,
  IqujiDiagnosticBanner,
  IqujiLoadingState,
  IqujiSettingsActionRow,
  IqujiSettingsContent,
  IqujiSettingsRow,
  IqujiSettingsSection,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { errorMessage, fetchJson } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { fetchLocalConfig } from "./api";

/** 控制面信息行内可复制值展示组件 */
function CopyableSettingValue({
  label,
  value,
  multiline = false,
}: {
  label: string;
  value: string;
  multiline?: boolean;
}) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // 忽略剪贴板不可用异常
    }
  };

  return (
    <div className={`ops-settings-value-copy ${multiline ? "is-multiline" : ""}`}>
      <code className="ops-settings-value-code" title={value}>
        {value}
      </code>
      <IconButton
        aria-label={copied ? t("common.copied") : t("common.copyTarget", { target: label })}
        compact
        icon={<IqujiIcon decorative name={copied ? "check" : "copy"} size={13} />}
        label={copied ? t("common.copied") : t("common.copyTarget", { target: label })}
        onClick={() => void handleCopy()}
        variant="ghost"
      />
    </div>
  );
}

/**
 * 设置 → 通用设置（控制面信息与本地服务状态）
 */
export function GeneralSettingsSection() {
  const { t } = useI18n();
  const configQuery = useQuery({ queryKey: ["local-config"], queryFn: fetchLocalConfig });
  const healthQuery = useQuery({
    queryKey: ["control-plane-health"],
    queryFn: () => fetchJson<{ ok: boolean }>("health"),
    retry: false,
  });

  const config = configQuery.data?.controlPlane;
  const endpoint = config ? `${config.apiHost}:${config.apiPort}` : "";
  const publicBaseUrl = config?.publicBaseUrl?.trim().replace(/\/+$/u, "") ?? "";
  const showExternalUrl =
    Boolean(publicBaseUrl) &&
    publicBaseUrl !== `http://${endpoint}` &&
    publicBaseUrl !== `https://${endpoint}`;

  const serverTone = healthQuery.data?.ok ? "success" : healthQuery.isError ? "danger" : "neutral";

  return (
    <IqujiSettingsContent description={t("settings.generalDesc")} title={t("settings.general")}>
      <IqujiSettingsSection title={t("settings.controlPlaneGroup")}>
        {configQuery.isError ? (
          <div className="ops-settings-banner-slot">
            <IqujiDiagnosticBanner
              action={
                <IqujiButton compact onClick={() => void configQuery.refetch()} variant="secondary">
                  {t("common.retry")}
                </IqujiButton>
              }
              description={errorMessage(configQuery.error, t)}
              title={t("settings.localConfigReadFailed")}
              tone="danger"
            />
          </div>
        ) : configQuery.isLoading ? (
          <div className="ops-settings-loading-slot">
            <IqujiLoadingState label={t("runtime.statusChecking")} />
          </div>
        ) : config ? (
          <>
            <IqujiSettingsRow
              control={
                <IqujiBadge className="ops-font-mono" tone="neutral">
                  iQuji Ops {config.version}
                </IqujiBadge>
              }
              label={t("settings.activeVersion")}
            />
            <IqujiSettingsRow
              control={
                <CopyableSettingValue label={t("settings.activeEndpoint")} value={endpoint} />
              }
              label={t("settings.activeEndpoint")}
            />
            {showExternalUrl ? (
              <IqujiSettingsRow
                control={
                  <CopyableSettingValue
                    label={t("settings.publicBaseUrl")}
                    multiline
                    value={publicBaseUrl}
                  />
                }
                label={t("settings.publicBaseUrl")}
              />
            ) : null}
            <IqujiSettingsRow
              align="start"
              control={
                <CopyableSettingValue
                  label={t("settings.activeDataDirectory")}
                  multiline
                  value={config.dataDirectory}
                />
              }
              label={t("settings.activeDataDirectory")}
            />
          </>
        ) : null}
      </IqujiSettingsSection>

      <IqujiSettingsSection title={t("runtime.panelTitle")}>
        {healthQuery.isError ? (
          <div className="ops-settings-banner-slot">
            <IqujiDiagnosticBanner
              action={
                <IqujiButton
                  compact
                  loading={healthQuery.isFetching}
                  onClick={() => void healthQuery.refetch()}
                  variant="secondary"
                >
                  {t("runtime.btnRecheck")}
                </IqujiButton>
              }
              description={errorMessage(healthQuery.error, t)}
              title={t("runtime.connectFailedAlert")}
              tone="danger"
            />
          </div>
        ) : null}

        <IqujiSettingsActionRow
          action={
            <IqujiButton
              compact
              loading={healthQuery.isFetching}
              onClick={() => void healthQuery.refetch()}
              variant="secondary"
            >
              {t("runtime.btnRecheck")}
            </IqujiButton>
          }
          label="iquops-server"
          value={
            <IqujiBadge tone={serverTone}>
              {healthQuery.data?.ok
                ? t("runtime.statusConnected")
                : healthQuery.isFetching
                  ? t("runtime.statusChecking")
                  : t("runtime.statusUnconfirmed")}
            </IqujiBadge>
          }
        />

        <IqujiSettingsActionRow
          label="iquops-worker"
          value={<IqujiBadge tone="neutral">{t("runtime.workerBadge")}</IqujiBadge>}
        />
      </IqujiSettingsSection>
    </IqujiSettingsContent>
  );
}

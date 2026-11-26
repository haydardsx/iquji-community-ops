import {
  IqujiButton,
  IqujiInput,
  IqujiSelect,
  IqujiSettingsContent,
  IqujiSettingsLayout,
  IqujiSettingsNav,
  type IqujiSettingsNavGroup,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { IqujiIcon, type IqujiIconName } from "@iquji-community/web-icons";
import { useCallback, useEffect, useMemo, useState } from "react";
import { AppearanceSettingsSection } from "@/features/settings/AppearanceSettingsSection";
import { ApplicationTemplatesSection } from "@/features/settings/ApplicationTemplatesSection";
import { AutomationSettings } from "@/features/settings/AutomationSettings";
import { BackupMaintenanceSection } from "@/features/settings/BackupMaintenanceSection";
import { DomainCertificatesSection } from "@/features/settings/DomainCertificatesSection";
import { GeneralSettingsSection } from "@/features/settings/GeneralSettingsSection";
import {
  desktopProxySettingsAvailable,
  type NetworkCredentialAction,
  type NetworkProxyMode,
  type NetworkProxySettings,
  readNetworkProxySettings,
  saveNetworkProxySettings,
} from "@/features/settings/network-proxy";
import { SecretsSettingsSection } from "@/features/settings/SecretsSettingsSection";
import { useAutomationScope } from "@/shared/desktop/automation-controls";
import { type OpsMessageKey, useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import { SettingFieldLabel } from "@/shared/ui/field-help";

type SettingsCategory =
  | "automation"
  | "appearance"
  | "general"
  | "secrets"
  | "domain-certificates"
  | "application-templates"
  | "network"
  | "backup";

interface SettingsCategoryConfig {
  id: SettingsCategory;
  labelKey: OpsMessageKey;
  icon: IqujiIconName;
  groupKey: "preferences" | "assets" | "network" | "system" | "integrations";
}

const CATEGORY_DEFS: SettingsCategoryConfig[] = [
  { id: "appearance", labelKey: "settings.appearance", icon: "sun", groupKey: "preferences" },
  { id: "general", labelKey: "settings.general", icon: "settings", groupKey: "preferences" },
  { id: "secrets", labelKey: "settings.secrets", icon: "key-round", groupKey: "assets" },
  { id: "application-templates", labelKey: "templates.title", icon: "boxes", groupKey: "assets" },
  {
    id: "domain-certificates",
    labelKey: "settings.domainCertificates",
    icon: "globe",
    groupKey: "assets",
  },
  { id: "network", labelKey: "settings.networkProxy", icon: "network", groupKey: "network" },
  {
    id: "backup",
    labelKey: "settings.backupMaintenance",
    icon: "hard-drive-download",
    groupKey: "system",
  },
  {
    id: "automation",
    labelKey: "automation.title",
    icon: "sliders-horizontal",
    groupKey: "integrations",
  },
];

export function SettingsPage() {
  const notification = useIqujiNotification();
  const { t } = useI18n();

  const [activeCategory, setActiveCategory] = useState<SettingsCategory>(() => {
    if (typeof window !== "undefined" && window.location.hash) {
      const hash = window.location.hash.replace("#", "");
      if (CATEGORY_DEFS.some((c) => c.id === hash)) {
        return hash as SettingsCategory;
      }
    }
    return "appearance";
  });

  useEffect(() => {
    const selectFromHash = () => {
      const hash = window.location.hash.slice(1);
      if (CATEGORY_DEFS.some((category) => category.id === hash))
        setActiveCategory(hash as SettingsCategory);
    };
    window.addEventListener("hashchange", selectFromHash);
    return () => window.removeEventListener("hashchange", selectFromHash);
  }, []);

  useAutomationScope("settings", "", activeCategory);
  const [searchQuery, setSearchQuery] = useState("");
  const [proxyMode, setProxyMode] = useState<NetworkProxyMode>("direct");
  const [proxyUrl, setProxyUrl] = useState("");
  const [proxyUsername, setProxyUsername] = useState("");
  const [proxyPassword, setProxyPassword] = useState("");
  const [credentialAction, setCredentialAction] = useState<NetworkCredentialAction>("keep");
  const [hasCredentials, setHasCredentials] = useState(false);
  const [proxyAvailable, setProxyAvailable] = useState(false);
  const [proxyLoading, setProxyLoading] = useState(true);
  const [proxySaving, setProxySaving] = useState(false);
  const [proxyError, setProxyError] = useState<string | null>(null);
  const [proxySaved, setProxySaved] = useState(false);

  const applyProxySettings = useCallback((settings: NetworkProxySettings) => {
    setProxyMode(settings.mode);
    setProxyUrl(settings.url ?? "");
    setHasCredentials(settings.hasCredentials);
    setCredentialAction("keep");
    setProxyUsername("");
    setProxyPassword("");
  }, []);

  useEffect(() => {
    const available = desktopProxySettingsAvailable();
    setProxyAvailable(available);
    if (!available) {
      setProxyLoading(false);
      return;
    }
    readNetworkProxySettings()
      .then(applyProxySettings)
      .catch((error: unknown) =>
        setProxyError(error instanceof Error ? error.message : t("network.readFailed")),
      )
      .finally(() => setProxyLoading(false));
  }, [applyProxySettings, t]);

  const saveProxy = async () => {
    if (!proxyAvailable || proxySaving) return;
    setProxySaving(true);
    setProxyError(null);
    setProxySaved(false);
    try {
      const settings = await saveNetworkProxySettings({
        mode: proxyMode,
        url: proxyMode === "manual" ? proxyUrl.trim() : null,
        credentials: {
          action: credentialAction,
          ...(credentialAction === "replace"
            ? { username: proxyUsername, password: proxyPassword }
            : {}),
        },
      });
      applyProxySettings(settings);
      setProxySaved(true);
      notification.open({ title: t("network.proxySaved"), tone: "success" });
    } catch (error) {
      notification.open({
        title: t("network.saveFailed"),
        description: error instanceof Error ? error.message : t("common.opFailed"),
        tone: "danger",
        duration: 0,
      });
    } finally {
      setProxySaving(false);
    }
  };

  const categories = useMemo(() => {
    return CATEGORY_DEFS.map((c) => ({
      ...c,
      label: t(c.labelKey),
    }));
  }, [t]);

  const navGroups = useMemo<IqujiSettingsNavGroup[]>(() => {
    const query = searchQuery.trim().toLowerCase();
    const groupDefs: Array<{
      key: "preferences" | "assets" | "network" | "system" | "integrations";
      label: string;
    }> = [
      { key: "preferences", label: t("settings.groupPreferences") },
      { key: "assets", label: t("settings.groupAssets") },
      { key: "network", label: t("settings.groupNetwork") },
      { key: "system", label: t("settings.groupSystem") },
      { key: "integrations", label: t("settings.groupIntegrations") },
    ];

    return groupDefs.flatMap((group) => {
      const matched = categories.filter(
        (c) => c.groupKey === group.key && (query ? c.label.toLowerCase().includes(query) : true),
      );
      const items = matched.map((category) => ({
        active: activeCategory === category.id,
        trailing:
          category.id === "secrets" || category.id === "domain-certificates" ? (
            <OperationActivityDot scope={{ category: category.id }} />
          ) : undefined,
        icon: <IqujiIcon decorative name={category.icon} />,
        id: category.id,
        key: category.id,
        label: <span data-ops-guide-category={category.id}>{category.label}</span>,
        onSelect: () => {
          setActiveCategory(category.id);
          if (typeof window !== "undefined") {
            window.location.hash = category.id;
          }
        },
      }));
      return items.length > 0 ? [{ key: group.key, label: group.label, items }] : [];
    });
  }, [activeCategory, categories, searchQuery, t]);

  return (
    <div className="ops-settings-fullscreen-container" data-ops-tutorial="workspace.settings">
      <IqujiSettingsLayout
        navigation={
          <IqujiSettingsNav
            emptyLabel={t("settings.emptySearch")}
            groups={navGroups}
            label={t("settings.navLabel")}
            search={{
              label: t("settings.searchLabel"),
              onChange: setSearchQuery,
              placeholder: t("settings.searchPlaceholder"),
              value: searchQuery,
            }}
          />
        }
      >
        {activeCategory === "appearance" ? <AppearanceSettingsSection /> : null}

        {activeCategory === "general" ? <GeneralSettingsSection /> : null}

        {activeCategory === "secrets" ? <SecretsSettingsSection /> : null}

        {activeCategory === "domain-certificates" ? <DomainCertificatesSection /> : null}
        {activeCategory === "application-templates" ? <ApplicationTemplatesSection /> : null}

        {activeCategory === "network" ? (
          <IqujiSettingsContent description={t("network.desc")} title={t("network.proxyTitle")}>
            <div className="ops-settings-group">
              <h3 className="ops-settings-group__title">{t("network.outboundPref")}</h3>
              <div className="ops-settings-card">
                <div className="ops-setting-row">
                  <div className="ops-setting-row__meta">
                    <SettingFieldLabel
                      label={t("network.proxyMode")}
                      help={t("network.proxyModeDesc")}
                    />
                  </div>
                  <div className="ops-setting-row__control">
                    <IqujiSelect
                      aria-label={t("network.proxyMode")}
                      disabled={!proxyAvailable || proxyLoading || proxySaving}
                      onValueChange={(value) => {
                        if (value) {
                          const nextMode = value as NetworkProxyMode;
                          setProxyMode(nextMode);
                          if (nextMode !== "manual" && credentialAction === "replace") {
                            setCredentialAction("keep");
                          }
                        }
                      }}
                      options={[
                        { label: t("network.proxyDirect"), value: "direct" },
                        { label: t("network.proxySystem"), value: "system" },
                        { label: `${t("network.proxyManual")} (HTTP/SOCKS5)`, value: "manual" },
                      ]}
                      value={proxyMode}
                    />
                  </div>
                </div>

                {proxyMode === "manual" ? (
                  <>
                    <div className="ops-setting-row">
                      <div className="ops-setting-row__meta">
                        <span className="ops-setting-row__label">{t("network.proxyServer")}</span>
                      </div>
                      <div className="ops-setting-row__control">
                        <IqujiInput
                          aria-label={t("network.proxyServer")}
                          disabled={!proxyAvailable || proxyLoading || proxySaving}
                          onChange={(event) => setProxyUrl(event.target.value)}
                          placeholder="http://127.0.0.1:7890"
                          value={proxyUrl}
                        />
                      </div>
                    </div>
                    <div className="ops-setting-row">
                      <div className="ops-setting-row__meta">
                        <SettingFieldLabel
                          label={t("network.proxyAuth")}
                          help={t("network.proxyAuthDesc")}
                        />
                      </div>
                      <div className="ops-setting-row__control">
                        <IqujiSelect
                          aria-label={t("network.proxyAuth")}
                          disabled={!proxyAvailable || proxyLoading || proxySaving}
                          onValueChange={(value) => {
                            if (value) {
                              setCredentialAction(value as NetworkCredentialAction);
                              setProxySaved(false);
                            }
                          }}
                          options={[
                            {
                              label: hasCredentials
                                ? t("network.proxyAuthKeep")
                                : t("network.proxyAuthKeep"),
                              value: "keep",
                            },
                            { label: t("network.proxyAuthReplace"), value: "replace" },
                            { label: t("network.proxyAuthClear"), value: "clear" },
                          ]}
                          value={credentialAction}
                        />
                      </div>
                    </div>
                    {credentialAction === "replace" ? (
                      <>
                        <div className="ops-setting-row">
                          <div className="ops-setting-row__meta">
                            <span className="ops-setting-row__label">{t("network.username")}</span>
                          </div>
                          <div className="ops-setting-row__control">
                            <IqujiInput
                              aria-label={t("network.username")}
                              autoComplete="username"
                              disabled={!proxyAvailable || proxyLoading || proxySaving}
                              onChange={(event) => setProxyUsername(event.target.value)}
                              value={proxyUsername}
                            />
                          </div>
                        </div>
                        <div className="ops-setting-row">
                          <div className="ops-setting-row__meta">
                            <span className="ops-setting-row__label">{t("network.password")}</span>
                          </div>
                          <div className="ops-setting-row__control">
                            <IqujiInput
                              aria-label={t("network.password")}
                              autoComplete="new-password"
                              disabled={!proxyAvailable || proxyLoading || proxySaving}
                              onChange={(event) => setProxyPassword(event.target.value)}
                              type="password"
                              value={proxyPassword}
                            />
                          </div>
                        </div>
                      </>
                    ) : null}
                  </>
                ) : null}

                <div className="ops-setting-row">
                  <div className="ops-setting-row__meta">
                    <span className="ops-setting-row__label">{t("common.save")}</span>
                    <span className="ops-setting-row__description">
                      {proxyAvailable ? t("network.proxySaved") : t("network.proxyUnavailable")}
                    </span>
                  </div>
                  <div className="ops-setting-row__control">
                    <IqujiButton
                      compact
                      disabled={!proxyAvailable || proxyLoading || proxySaving}
                      onClick={saveProxy}
                      variant="primary"
                    >
                      {proxySaving
                        ? t("common.saving")
                        : proxySaved
                          ? t("common.opCompleted")
                          : t("network.saveProxy")}
                    </IqujiButton>
                  </div>
                </div>
              </div>
            </div>
            {proxyError ? <p role="alert">{proxyError}</p> : null}
          </IqujiSettingsContent>
        ) : null}

        {activeCategory === "backup" ? <BackupMaintenanceSection /> : null}

        {activeCategory === "automation" ? <AutomationSettings /> : null}
      </IqujiSettingsLayout>
    </div>
  );
}

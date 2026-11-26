import {
  IqujiButton,
  IqujiSelect,
  IqujiSettingsContent,
  IqujiSettingsSection,
} from "@iquji-community/web-component";
import { useIqujiTheme } from "@iquji-community/web-theme";
import { useAutomationScope } from "@/shared/desktop/automation-controls";
import { type Locale, useI18n } from "@/shared/i18n";
import { SettingFieldLabel } from "@/shared/ui/field-help";

export function AppearanceSettingsSection() {
  const { mode: themeMode, setMode: setThemeMode } = useIqujiTheme();
  const { locale, setLocale, t } = useI18n();

  useAutomationScope("appearance", "", "appearance", undefined, {
    theme: {
      value: themeMode,
      run: (value) => {
        if (value !== "system" && value !== "dark" && value !== "light")
          throw Error("AUTOMATION_FIELD_INVALID");
        setThemeMode(value);
      },
    },
    locale: {
      value: locale,
      run: (value) => {
        if (value !== "zh-CN" && value !== "en-US") throw Error("AUTOMATION_FIELD_INVALID");
        setLocale(value);
      },
    },
  });
  return (
    <IqujiSettingsContent
      description={t("settings.appearanceDesc")}
      title={t("settings.appearance")}
    >
      <IqujiSettingsSection
        className="ops-theme-section"
        description={t("settings.themeDesc")}
        title={t("settings.theme")}
      >
        <div data-ops-guide-control="appearance.theme" className="ops-theme-cards-grid">
          <IqujiButton
            allowLabelWrap
            className={`ops-theme-card ${themeMode === "system" ? "is-active" : ""}`}
            compact
            onClick={() => setThemeMode("system")}
            variant="ghost"
          >
            <div className="ops-theme-card-preview">
              <div className="theme-mock-system">
                <div className="theme-mock-left">
                  <div className="theme-mock-bar-dark" style={{ width: "70%" }} />
                  <div className="theme-mock-bar-dark" style={{ width: "90%" }} />
                  <div className="theme-mock-bar-dark" style={{ width: "60%" }} />
                </div>
                <div className="theme-mock-right">
                  <div className="theme-mock-bar-light" style={{ width: "80%" }} />
                  <div className="theme-mock-bar-light" style={{ width: "60%" }} />
                  <div className="theme-mock-bar-light" style={{ width: "90%" }} />
                </div>
              </div>
            </div>
            <span className="ops-theme-card-label">{t("settings.themeSystem")}</span>
          </IqujiButton>

          <IqujiButton
            allowLabelWrap
            className={`ops-theme-card ${themeMode === "light" ? "is-active" : ""}`}
            compact
            onClick={() => setThemeMode("light")}
            variant="ghost"
          >
            <div className="ops-theme-card-preview">
              <div className="theme-mock-light">
                <div className="theme-mock-bar-light" style={{ width: "60%" }} />
                <div className="theme-mock-bar-light" style={{ width: "85%" }} />
                <div className="theme-mock-bar-light" style={{ width: "70%" }} />
              </div>
            </div>
            <span className="ops-theme-card-label">{t("settings.themeLight")}</span>
          </IqujiButton>

          <IqujiButton
            allowLabelWrap
            className={`ops-theme-card ${themeMode === "dark" ? "is-active" : ""}`}
            compact
            onClick={() => setThemeMode("dark")}
            variant="ghost"
          >
            <div className="ops-theme-card-preview">
              <div className="theme-mock-dark">
                <div className="theme-mock-bar-dark" style={{ width: "60%" }} />
                <div className="theme-mock-bar-dark" style={{ width: "85%" }} />
                <div className="theme-mock-bar-dark" style={{ width: "70%" }} />
              </div>
            </div>
            <span className="ops-theme-card-label">{t("settings.themeDark")}</span>
          </IqujiButton>
        </div>
      </IqujiSettingsSection>

      <div className="ops-settings-group" style={{ marginTop: "var(--iquji-space-4)" }}>
        <h3 className="ops-settings-group__title">{t("settings.interface")}</h3>
        <div className="ops-settings-card">
          <div className="ops-setting-row">
            <div className="ops-setting-row__meta">
              <SettingFieldLabel label={t("settings.language")} help={t("settings.languageDesc")} />
            </div>
            <div className="ops-setting-row__control">
              <IqujiSelect
                data-ops-guide-control="appearance.locale"
                aria-label={t("settings.language")}
                onValueChange={(val) => val && setLocale(val as Locale)}
                options={[
                  { label: t("settings.langZhCN"), value: "zh-CN" },
                  { label: t("settings.langEnUS"), value: "en-US" },
                ]}
                value={locale}
              />
            </div>
          </div>
        </div>
      </div>
    </IqujiSettingsContent>
  );
}

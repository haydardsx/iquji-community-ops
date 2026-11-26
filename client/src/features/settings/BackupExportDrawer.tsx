import {
  IqujiBadge,
  IqujiButton,
  IqujiDiagnosticBanner,
  IqujiField,
  IqujiInput,
  IqujiSettingsSwitchRow,
} from "@iquji-community/web-component";
import { type ChangeEvent, useState } from "react";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { useOperationNotice } from "@/shared/ui/use-operation-feedback";
import {
  type BackupOptions,
  type BackupPreview,
  type BackupScope,
  backupScopeValues,
  downloadBackup,
  previewBackup,
} from "./api";
import { formatScopeLabel } from "./backup-maintenance-format";
import {
  BackupDependencyList,
  BackupDiagnosticBanners,
  BackupRecordCounts,
  BackupScopeGrid,
} from "./backup-maintenance-ui";
import "./backup-maintenance.css";

const MIN_PASSPHRASE_LENGTH = 12;

type BackupExportDrawerProps = {
  open: boolean;
  onClose: () => void;
};

/** 导出备份抽屉：选择范围、可选加密口令与预览后下载归档 */
export function BackupExportDrawer({ open, onClose }: BackupExportDrawerProps) {
  const { t } = useI18n();
  const notice = useOperationNotice(t("backup.exportRowLabel"));

  const [requestedScopes, setRequestedScopes] = useState<BackupScope[]>([...backupScopeValues]);
  const [includeSensitiveData, setIncludeSensitiveData] = useState(false);
  const [includeLogsAndAudit, setIncludeLogsAndAudit] = useState(false);
  const [passphrase, setPassphrase] = useState("");
  const [preview, setPreview] = useState<BackupPreview | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const options: BackupOptions = {
    requestedScopes,
    includeSensitiveData,
    includeLogsAndAudit,
  };

  const resetTransient = () => {
    setPreview(null);
    setErrorMsg(null);
  };

  const handleClose = () => {
    if (downloading) return;
    setPassphrase("");
    setErrorMsg(null);
    onClose();
  };

  const toggleScope = (scope: BackupScope, checked: boolean) => {
    setRequestedScopes((prev) =>
      checked ? Array.from(new Set([...prev, scope])) : prev.filter((item) => item !== scope),
    );
    resetTransient();
  };

  const handleSensitiveChange = (event: ChangeEvent<HTMLInputElement>) => {
    const next = event.target.checked;
    setIncludeSensitiveData(next);
    if (!next) setPassphrase("");
    resetTransient();
  };

  const handleLogsChange = (event: ChangeEvent<HTMLInputElement>) => {
    setIncludeLogsAndAudit(event.target.checked);
    resetTransient();
  };

  const handlePreview = async () => {
    setPreviewing(true);
    setErrorMsg(null);
    try {
      const next = await previewBackup(options);
      setPreview(next);
    } catch (error) {
      setErrorMsg(errorMessage(error, t));
    } finally {
      setPreviewing(false);
    }
  };

  const handleDownload = async () => {
    if (!preview) return;
    setDownloading(true);
    setErrorMsg(null);
    try {
      const { blob, filename } = await downloadBackup({
        options,
        previewDigest: preview.previewDigest,
        passphrase: includeSensitiveData ? passphrase : "",
      });
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = filename;
      link.click();
      URL.revokeObjectURL(url);
      setPassphrase("");
      setPreview(null);
      onClose();
      notice(t("backup.exportSuccess"));
    } catch (error) {
      setErrorMsg(errorMessage(error, t));
    } finally {
      setDownloading(false);
    }
  };

  const passphraseError =
    includeSensitiveData && passphrase.length > 0 && passphrase.length < MIN_PASSPHRASE_LENGTH
      ? t("backup.passphraseTooShort")
      : undefined;

  const canDownload =
    preview !== null &&
    preview.blockers.length === 0 &&
    !previewing &&
    !downloading &&
    (!includeSensitiveData || passphrase.length >= MIN_PASSPHRASE_LENGTH);

  const allSelected = requestedScopes.length === backupScopeValues.length;

  return (
    <InspectorDrawer
      actionBarLayout="end"
      description={t("backup.exportDrawerDesc")}
      footer={
        <>
          <IqujiButton
            disabled={requestedScopes.length === 0 || downloading}
            loading={previewing}
            onClick={() => void handlePreview()}
            variant="secondary"
          >
            {t("backup.previewBtn")}
          </IqujiButton>
          <IqujiButton
            disabled={!canDownload}
            loading={downloading}
            onClick={() => void handleDownload()}
            variant="primary"
          >
            {t("backup.downloadBtn")}
          </IqujiButton>
        </>
      }
      onClose={handleClose}
      open={open}
      size="md"
      title={t("backup.exportDrawerTitle")}
    >
      <div className="ops-form-stack">
        {errorMsg ? <IqujiDiagnosticBanner title={errorMsg} tone="danger" /> : null}

        <section className="ops-form-section">
          <div className="ops-bm-section-head">
            <h3 className="ops-form-section__title">{t("backup.scopeTitle")}</h3>
            <IqujiButton
              compact
              onClick={() => {
                setRequestedScopes(allSelected ? [] : [...backupScopeValues]);
                resetTransient();
              }}
              variant="ghost"
            >
              {allSelected ? t("backup.clearScopes") : t("backup.selectAllScopes")}
            </IqujiButton>
          </div>
          <p className="ops-form-section__description">{t("backup.scopeDesc")}</p>
          <BackupScopeGrid
            onToggle={toggleScope}
            scopes={backupScopeValues}
            selected={requestedScopes}
          />
        </section>

        <section className="ops-form-section">
          <h3 className="ops-form-section__title">{t("backup.optionsSectionTitle")}</h3>
          <div className="ops-bm-switch-list">
            <IqujiSettingsSwitchRow
              checked={includeSensitiveData}
              description={t("backup.includeSecretsDesc")}
              label={t("backup.includeSecrets")}
              onChange={handleSensitiveChange}
            />
            {includeSensitiveData ? (
              <div className="ops-bm-switch-nested">
                <IqujiField
                  description={t("backup.passphraseDesc")}
                  error={passphraseError}
                  id="ops-backup-export-passphrase"
                  label={t("backup.passphraseLabel")}
                  required
                >
                  <IqujiInput
                    autoComplete="new-password"
                    id="ops-backup-export-passphrase"
                    onChange={(event) => setPassphrase(event.target.value)}
                    placeholder={t("backup.passphrasePlaceholder")}
                    type="password"
                    value={passphrase}
                  />
                </IqujiField>
              </div>
            ) : null}
            <IqujiSettingsSwitchRow
              checked={includeLogsAndAudit}
              description={t("backup.includeLogsDesc")}
              label={t("backup.includeLogs")}
              onChange={handleLogsChange}
            />
          </div>
        </section>

        {preview ? (
          <section className="ops-form-section">
            <h3 className="ops-form-section__title">{t("backup.previewTitle")}</h3>

            <BackupDiagnosticBanners blockers={preview.blockers} warnings={preview.warnings} />

            <dl className="ops-detail-meta-grid">
              <div className="ops-detail-meta-item">
                <dt>{t("backup.appVersionLabel")}</dt>
                <dd className="ops-font-mono">{preview.manifest.applicationVersion}</dd>
              </div>
              <div className="ops-detail-meta-item">
                <dt>{t("backup.schemaVersionLabel")}</dt>
                <dd className="ops-font-mono">{preview.manifest.schemaVersion}</dd>
              </div>
            </dl>

            <div className="ops-form-stack">
              <p className="ops-form-section__description">{t("backup.effectiveScopesLabel")}</p>
              <div className="ops-bm-badge-row">
                {preview.manifest.effectiveScopes.map((scope) => (
                  <IqujiBadge key={scope} tone="info">
                    {formatScopeLabel(scope, t)}
                  </IqujiBadge>
                ))}
              </div>
            </div>

            <BackupDependencyList
              dependencies={preview.manifest.dependencyReasons}
              title={t("backup.dependenciesTitle")}
            />

            <BackupRecordCounts
              counts={preview.manifest.recordCounts}
              emptyLabel={t("maintenance.noRecordsAffected")}
              title={t("backup.recordCountsTitle")}
            />
          </section>
        ) : null}
      </div>
    </InspectorDrawer>
  );
}

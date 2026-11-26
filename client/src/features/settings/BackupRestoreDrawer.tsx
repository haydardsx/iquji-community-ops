import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiDiagnosticBanner,
  IqujiField,
  IqujiFormSteps,
  IqujiInput,
  IqujiSettingsSelectRow,
  IqujiSettingsSwitchRow,
} from "@iquji-community/web-component";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { toOperationRun } from "@/shared/operations/task-response";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { FileSelector } from "@/shared/ui/file-selector";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { useOperationNotice } from "@/shared/ui/use-operation-feedback";
import {
  type BackupScope,
  executeRestore,
  previewRestore,
  type RestorePreview,
  type RestoreUpload,
  uploadRestore,
} from "./api";
import { formatArchiveTimestamp, formatScopeLabel } from "./backup-maintenance-format";
import {
  BackupDependencyList,
  BackupDiagnosticBanners,
  BackupScopeGrid,
} from "./backup-maintenance-ui";
import "./backup-maintenance.css";

type RestoreStepId = "upload" | "scope" | "execute";
type ConflictPolicy = "abort_on_conflict" | "overwrite_selected";

type BackupRestoreDrawerProps = {
  open: boolean;
  onClose: () => void;
};

/** 从备份恢复抽屉：分步完成文件解析、范围与冲突策略配置、影响核验与提交 */
export function BackupRestoreDrawer({ open, onClose }: BackupRestoreDrawerProps) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const notice = useOperationNotice(t("backup.restoreRowLabel"));
  const operation = useConfirmedOperation();

  const [step, setStep] = useState<RestoreStepId>("upload");
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [passphrase, setPassphrase] = useState("");
  const [uploadResult, setUploadResult] = useState<RestoreUpload | null>(null);
  const [selectedScopes, setSelectedScopes] = useState<BackupScope[]>([]);
  const [includeLogsAndAudit, setIncludeLogsAndAudit] = useState(false);
  const [conflictPolicy, setConflictPolicy] = useState<ConflictPolicy>("abort_on_conflict");
  const [preview, setPreview] = useState<RestorePreview | null>(null);
  const [uploading, setUploading] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const busy = uploading || previewing || operation.isPending;

  const resetAll = () => {
    setStep("upload");
    setSelectedFile(null);
    setPassphrase("");
    setUploadResult(null);
    setSelectedScopes([]);
    setIncludeLogsAndAudit(false);
    setConflictPolicy("abort_on_conflict");
    setPreview(null);
    setErrorMsg(null);
    operation.reset();
  };

  const handleClose = () => {
    if (busy) return;
    resetAll();
    onClose();
  };

  const handleUpload = async () => {
    if (!selectedFile) return;
    setUploading(true);
    setErrorMsg(null);
    try {
      const uploaded = await uploadRestore(selectedFile, passphrase);
      const initialScopes =
        uploaded.manifest.effectiveScopes.length > 0
          ? uploaded.manifest.effectiveScopes
          : uploaded.manifest.requestedScopes;
      setUploadResult(uploaded);
      setSelectedScopes([...initialScopes]);
      setIncludeLogsAndAudit(uploaded.manifest.includesLogsAndAudit);
      setPreview(null);
      setStep("scope");
    } catch (error) {
      setErrorMsg(errorMessage(error, t));
    } finally {
      setUploading(false);
    }
  };

  const handlePreview = async () => {
    if (!uploadResult) return;
    setPreviewing(true);
    setErrorMsg(null);
    try {
      const next = await previewRestore({
        uploadId: uploadResult.uploadId,
        options: {
          requestedScopes: selectedScopes,
          includeLogsAndAudit,
          conflictPolicy,
        },
      });
      setPreview(next);
      setStep("execute");
    } catch (error) {
      setErrorMsg(errorMessage(error, t));
    } finally {
      setPreviewing(false);
    }
  };

  const handleExecute = async () => {
    if (!uploadResult || !preview) return;
    setErrorMsg(null);
    try {
      const action =
        conflictPolicy === "overwrite_selected" ? "restore_backup_overwrite" : "restore_backup";
      await operation.submit({
        action,
        targetKind: "backup_restore",
        targetId: uploadResult.uploadId,
        label: t("backup.restoreActionLabel"),
        create: async (requestKey) =>
          toOperationRun(
            await executeRestore(
              {
                uploadId: uploadResult.uploadId,
                options: {
                  requestedScopes: selectedScopes,
                  includeLogsAndAudit,
                  conflictPolicy,
                },
                previewDigest: preview.previewDigest,
              },
              requestKey,
            ),
          ),
      });
      await queryClient.invalidateQueries();
      resetAll();
      onClose();
      notice(t("backup.restoreSucceededNotice"));
    } catch (error) {
      setErrorMsg(errorMessage(error, t));
    }
  };

  const toggleScope = (scope: BackupScope, checked: boolean) => {
    setSelectedScopes((prev) =>
      checked ? Array.from(new Set([...prev, scope])) : prev.filter((item) => item !== scope),
    );
    setPreview(null);
  };

  const steps = [
    {
      id: "upload" as const,
      label: t("backup.stepUpload"),
      description: t("backup.stepUploadDesc"),
      completed: uploadResult !== null,
    },
    {
      id: "scope" as const,
      label: t("backup.stepOptions"),
      description: t("backup.stepOptionsDesc"),
      disabled: uploadResult === null,
      completed: preview !== null,
    },
    {
      id: "execute" as const,
      label: t("backup.stepConfirm"),
      description: t("backup.stepConfirmDesc"),
      disabled: preview === null,
    },
  ];

  const archiveScopes = uploadResult?.manifest.effectiveScopes ?? [];

  const renderFooter = () => {
    if (step === "upload") {
      return (
        <IqujiButton
          disabled={!selectedFile || busy}
          loading={uploading}
          onClick={() => void handleUpload()}
          variant="primary"
        >
          {t("backup.uploadAndVerifyBtn")}
        </IqujiButton>
      );
    }
    if (step === "scope") {
      return (
        <>
          <IqujiButton disabled={busy} onClick={() => setStep("upload")} variant="secondary">
            {t("backup.prevStepBtn")}
          </IqujiButton>
          <IqujiButton
            disabled={selectedScopes.length === 0 || busy}
            loading={previewing}
            onClick={() => void handlePreview()}
            variant="primary"
          >
            {t("backup.previewRestoreBtn")}
          </IqujiButton>
        </>
      );
    }
    return (
      <>
        <IqujiButton disabled={busy} onClick={() => setStep("scope")} variant="secondary">
          {t("backup.prevStepBtn")}
        </IqujiButton>
        <IqujiButton
          disabled={!preview || preview.blockers.length > 0 || busy}
          loading={operation.isPending}
          onClick={() => void handleExecute()}
          variant="primary"
        >
          {t("backup.executeRestoreBtn")}
        </IqujiButton>
      </>
    );
  };

  return (
    <InspectorDrawer
      actionBarLayout="end"
      description={t("backup.restoreDrawerDesc")}
      footer={renderFooter()}
      onClose={handleClose}
      open={open}
      size="lg"
      title={t("backup.restoreDrawerTitle")}
    >
      <div className="ops-form-stack">
        <IqujiFormSteps
          label={t("backup.stepsAriaLabel")}
          onChange={(next) => {
            if (!busy) setStep(next);
          }}
          steps={steps}
          value={step}
        />

        {errorMsg ? <IqujiDiagnosticBanner title={errorMsg} tone="danger" /> : null}

        {step === "upload" ? (
          <div className="ops-form-stack">
            <section className="ops-form-section">
              <h3 className="ops-form-section__title">{t("backup.selectFileLabel")}</h3>
              <p className="ops-form-section__description">{t("backup.selectFileHint")}</p>
              <FileSelector
                accept=".iquops-backup,.iquops"
                ariaLabel={t("backup.selectFileLabel")}
                buttonLabel={t("backup.selectFileButton")}
                disabled={busy}
                file={selectedFile}
                fullWidth
                onFileChange={(file) => {
                  setSelectedFile(file);
                  setErrorMsg(null);
                }}
                placeholder={t("backup.selectFilePlaceholder")}
              />
            </section>

            <section className="ops-form-section">
              <IqujiField
                description={t("backup.restorePassphraseDesc")}
                id="ops-backup-restore-passphrase"
                label={t("backup.restorePassphraseLabel")}
              >
                <IqujiInput
                  autoComplete="off"
                  id="ops-backup-restore-passphrase"
                  onChange={(event) => setPassphrase(event.target.value)}
                  placeholder={t("backup.restorePassphrasePlaceholder")}
                  type="password"
                  value={passphrase}
                />
              </IqujiField>
            </section>
          </div>
        ) : null}

        {step === "scope" && uploadResult ? (
          <div className="ops-form-stack">
            <section className="ops-form-section">
              <h3 className="ops-form-section__title">{t("backup.uploadedArchiveTitle")}</h3>
              <dl className="ops-detail-meta-grid">
                <div className="ops-detail-meta-item">
                  <dt>{t("backup.appVersionLabel")}</dt>
                  <dd className="ops-font-mono">{uploadResult.manifest.applicationVersion}</dd>
                </div>
                <div className="ops-detail-meta-item">
                  <dt>{t("backup.schemaVersionLabel")}</dt>
                  <dd className="ops-font-mono">{uploadResult.manifest.schemaVersion}</dd>
                </div>
                <div className="ops-detail-meta-item">
                  <dt>{t("backup.archiveCreatedAt")}</dt>
                  <dd>{formatArchiveTimestamp(uploadResult.manifest.createdAt)}</dd>
                </div>
                <div className="ops-detail-meta-item">
                  <dt>{t("backup.archiveContainsSecrets")}</dt>
                  <dd>
                    {uploadResult.manifest.includesSensitiveData
                      ? t("backup.boolYes")
                      : t("backup.boolNo")}
                  </dd>
                </div>
                <div className="ops-detail-meta-item">
                  <dt>{t("backup.archiveContainsLogs")}</dt>
                  <dd>
                    {uploadResult.manifest.includesLogsAndAudit
                      ? t("backup.boolYes")
                      : t("backup.boolNo")}
                  </dd>
                </div>
              </dl>
            </section>

            <section className="ops-form-section">
              <h3 className="ops-form-section__title">{t("backup.restoreScopesLabel")}</h3>
              <BackupScopeGrid
                onToggle={toggleScope}
                scopes={archiveScopes}
                selected={selectedScopes}
              />
            </section>

            <section className="ops-form-section">
              <div className="ops-bm-switch-list">
                <IqujiSettingsSwitchRow
                  checked={includeLogsAndAudit}
                  description={
                    uploadResult.manifest.includesLogsAndAudit
                      ? t("backup.restoreIncludeLogsDesc")
                      : t("backup.restoreLogsUnavailableDesc")
                  }
                  disabled={!uploadResult.manifest.includesLogsAndAudit}
                  label={t("backup.includeLogs")}
                  onChange={(event) => {
                    setIncludeLogsAndAudit(event.target.checked);
                    setPreview(null);
                  }}
                />
                <IqujiSettingsSelectRow
                  description={t("backup.conflictPolicyDesc")}
                  label={t("backup.conflictPolicyLabel")}
                  onValueChange={(value) => {
                    if (value === "abort_on_conflict" || value === "overwrite_selected") {
                      setConflictPolicy(value);
                      setPreview(null);
                    }
                  }}
                  options={[
                    { label: t("backup.conflictAbort"), value: "abort_on_conflict" },
                    { label: t("backup.conflictOverwrite"), value: "overwrite_selected" },
                  ]}
                  value={conflictPolicy}
                />
              </div>
              {conflictPolicy === "overwrite_selected" ? (
                <IqujiDiagnosticBanner
                  compact
                  title={t("backup.conflictOverwriteAlert")}
                  tone="warning"
                />
              ) : null}
            </section>
          </div>
        ) : null}

        {step === "execute" && preview ? (
          <div className="ops-form-stack">
            <BackupDiagnosticBanners blockers={preview.blockers} warnings={preview.warnings} />

            {preview.blockers.length === 0 && preview.conflicts.length === 0 ? (
              <IqujiDiagnosticBanner
                compact
                title={t("backup.restoreNoConflictsTitle")}
                tone="success"
              />
            ) : null}

            <section className="ops-form-section">
              <h3 className="ops-form-section__title">{t("backup.selectedScopesLabel")}</h3>
              <div className="ops-bm-badge-row">
                {preview.selectedScopes.map((scope) => (
                  <IqujiBadge key={scope} tone="info">
                    {formatScopeLabel(scope, t)}
                  </IqujiBadge>
                ))}
              </div>
            </section>

            {preview.dependencyReasons.length > 0 ? (
              <section className="ops-form-section">
                <h3 className="ops-form-section__title">{t("backup.dependenciesTitle")}</h3>
                <BackupDependencyList dependencies={preview.dependencyReasons} title="" />
              </section>
            ) : null}

            {preview.conflicts.length > 0 ? (
              <section className="ops-form-section">
                <h3 className="ops-form-section__title">{t("backup.conflictsTitle")}</h3>
                <ul className="ops-flat-list ops-bm-plain-list">
                  {preview.conflicts.map((conflict) => (
                    <li className="ops-flat-row" key={conflict.id}>
                      <span>{conflict.summary || conflict.kind}</span>
                      <span className="ops-bm-conflict-key">{conflict.naturalKey}</span>
                    </li>
                  ))}
                </ul>
              </section>
            ) : null}

            {preview.missingSecretRefs.length > 0 ? (
              <section className="ops-form-section">
                <IqujiAlert title={t("backup.missingSecretsTitle")} tone="warning">
                  <div className="ops-form-stack">
                    <span>{t("backup.missingSecretsDesc")}</span>
                    <div className="ops-bm-badge-row">
                      {preview.missingSecretRefs.map((ref) => (
                        <IqujiBadge className="ops-font-mono" key={ref} tone="warning">
                          {ref}
                        </IqujiBadge>
                      ))}
                    </div>
                  </div>
                </IqujiAlert>
              </section>
            ) : null}
          </div>
        ) : null}
      </div>
    </InspectorDrawer>
  );
}

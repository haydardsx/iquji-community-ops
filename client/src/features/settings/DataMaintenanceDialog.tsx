import {
  IconButton,
  IqujiAlert,
  IqujiButton,
  IqujiDiagnosticBanner,
  IqujiDialog,
  IqujiField,
  IqujiInput,
  IqujiLoadingState,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useState } from "react";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { useOperationNotice } from "@/shared/ui/use-operation-feedback";
import {
  type DataMaintenanceAction,
  type DataMaintenancePreview,
  executeDataMaintenance,
  previewDataMaintenance,
} from "./api";
import { nonZeroCountEntries, resetClientAfterFactoryReset } from "./backup-maintenance-format";
import { BackupDiagnosticBanners, BackupRecordCounts } from "./backup-maintenance-ui";
import "./backup-maintenance.css";

type DataMaintenanceDialogProps = {
  action: DataMaintenanceAction | null;
  onClose: () => void;
  onFactoryResetComplete?: () => void;
};

/** 本地数据维护二次确认弹窗：先展示影响清单与阻断检查，再输入确认短语 */
export function DataMaintenanceDialog({
  action,
  onClose,
  onFactoryResetComplete,
}: DataMaintenanceDialogProps) {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const notice = useOperationNotice(t("maintenance.sectionTitle"));

  const [preview, setPreview] = useState<DataMaintenancePreview | null>(null);
  const [loading, setLoading] = useState(false);
  const [executing, setExecuting] = useState(false);
  const [phraseInput, setPhraseInput] = useState("");
  const [copiedPhrase, setCopiedPhrase] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const loadPreview = useCallback(
    async (targetAction: DataMaintenanceAction) => {
      setLoading(true);
      setErrorMsg(null);
      setPreview(null);
      setPhraseInput("");
      setCopiedPhrase(false);
      try {
        const result = await previewDataMaintenance(targetAction);
        setPreview(result);
      } catch (error) {
        setErrorMsg(errorMessage(error, t));
      } finally {
        setLoading(false);
      }
    },
    [t],
  );

  useEffect(() => {
    if (!action) {
      setPreview(null);
      setPhraseInput("");
      setCopiedPhrase(false);
      setErrorMsg(null);
      return;
    }
    void loadPreview(action);
  }, [action, loadPreview]);

  const isFactoryReset = action === "factory_reset";

  const handleConfirm = async () => {
    if (!action || !preview) return;
    setExecuting(true);
    setErrorMsg(null);
    try {
      await executeDataMaintenance(action, {
        confirmationToken: preview.confirmationToken,
        stateDigest: preview.stateDigest,
        confirmationPhrase: phraseInput,
      });
      if (action === "factory_reset") {
        resetClientAfterFactoryReset(queryClient);
        onClose();
        if (onFactoryResetComplete) {
          onFactoryResetComplete();
        } else {
          window.location.assign("/");
        }
        return;
      }
      await queryClient.invalidateQueries();
      onClose();
      notice(t("maintenance.clearLogsSuccess"));
    } catch (error) {
      setErrorMsg(errorMessage(error, t));
    } finally {
      setExecuting(false);
    }
  };

  const handleCopyPhrase = async () => {
    if (!preview?.confirmationPhrase) return;
    try {
      await navigator.clipboard.writeText(preview.confirmationPhrase);
      setCopiedPhrase(true);
      setTimeout(() => setCopiedPhrase(false), 1500);
    } catch {
      // 忽略剪贴板受限异常
    }
  };

  const hasPreserveItems = preview ? nonZeroCountEntries(preview.preserveCounts).length > 0 : false;
  const phraseMismatch =
    preview !== null && phraseInput.length > 0 && phraseInput !== preview.confirmationPhrase
      ? t("maintenance.phraseMismatch")
      : undefined;

  const canConfirm =
    preview !== null &&
    preview.blockers.length === 0 &&
    phraseInput === preview.confirmationPhrase &&
    !loading &&
    !executing;

  return (
    <IqujiDialog
      actions={
        <>
          <IqujiButton disabled={executing} onClick={() => onClose()} variant="secondary">
            {t("common.cancel")}
          </IqujiButton>
          <IqujiButton
            disabled={!canConfirm}
            loading={executing}
            onClick={() => void handleConfirm()}
            variant={isFactoryReset ? "danger" : "primary"}
          >
            {isFactoryReset
              ? t("maintenance.confirmFactoryResetBtn")
              : t("maintenance.confirmClearLogsBtn")}
          </IqujiButton>
        </>
      }
      closeLabel={t("common.close")}
      destroyOnClose
      mandatory={executing}
      onOpenChange={(next) => {
        if (!next && !executing) onClose();
      }}
      open={action !== null}
      size="md"
      title={isFactoryReset ? t("maintenance.factoryResetTitle") : t("maintenance.clearLogsTitle")}
    >
      <div className="ops-form-stack">
        <IqujiAlert
          title={
            isFactoryReset
              ? t("maintenance.factoryResetBannerTitle")
              : t("maintenance.clearLogsBannerTitle")
          }
          tone={isFactoryReset ? "danger" : "warning"}
        >
          {isFactoryReset
            ? t("maintenance.factoryResetBannerDesc")
            : t("maintenance.clearLogsBannerDesc")}
        </IqujiAlert>

        {errorMsg ? (
          <IqujiDiagnosticBanner
            action={
              action ? (
                <IqujiButton compact onClick={() => void loadPreview(action)} variant="secondary">
                  {t("common.retry")}
                </IqujiButton>
              ) : undefined
            }
            description={errorMsg}
            title={t("maintenance.previewFailed")}
            tone="danger"
          />
        ) : null}

        {loading ? (
          <div className="ops-bm-loading">
            <IqujiLoadingState label={t("maintenance.loadingPreview")} />
          </div>
        ) : null}

        {preview ? (
          <>
            <BackupDiagnosticBanners blockers={preview.blockers} warnings={preview.warnings} />

            <section className="ops-form-section">
              <h3 className="ops-form-section__title">{t("maintenance.deleteCountsTitle")}</h3>
              <BackupRecordCounts
                counts={preview.deleteCounts}
                emptyLabel={t("maintenance.noRecordsAffected")}
              />
            </section>

            {hasPreserveItems ? (
              <section className="ops-form-section">
                <h3 className="ops-form-section__title">{t("maintenance.preserveCountsTitle")}</h3>
                <BackupRecordCounts counts={preview.preserveCounts} />
              </section>
            ) : null}

            <section className="ops-form-section">
              <IqujiField
                description={
                  <div>
                    <span>{t("maintenance.phraseDesc")}</span>
                    <div>
                      <span className="ops-bm-phrase-box">
                        <code className="ops-bm-phrase-code">{preview.confirmationPhrase}</code>
                        <IconButton
                          compact
                          icon={
                            <IqujiIcon
                              decorative
                              name={copiedPhrase ? "check" : "copy"}
                              size={12}
                            />
                          }
                          label={t(copiedPhrase ? "common.copied" : "common.copy")}
                          onClick={() => void handleCopyPhrase()}
                          variant="ghost"
                        />
                      </span>
                    </div>
                  </div>
                }
                error={phraseMismatch}
                id="ops-maintenance-confirm-phrase"
                label={t("maintenance.phraseLabel")}
                required
              >
                <IqujiInput
                  autoComplete="off"
                  id="ops-maintenance-confirm-phrase"
                  onChange={(event) => setPhraseInput(event.target.value)}
                  placeholder={t("maintenance.phrasePlaceholder")}
                  value={phraseInput}
                />
              </IqujiField>
            </section>
          </>
        ) : null}
      </div>
    </IqujiDialog>
  );
}

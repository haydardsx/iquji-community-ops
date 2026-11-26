import { IqujiBadge, IqujiButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import type { OperationRun } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { operationActionLabel } from "@/shared/ui/operation-history";

interface ServerActiveRunsBannerProps {
  runs: OperationRun[];
  disabled?: boolean;
  onCancel: (run: OperationRun) => void;
}

export function ServerActiveRunsBanner({
  runs,
  disabled = false,
  onCancel,
}: ServerActiveRunsBannerProps) {
  const { t } = useI18n();

  if (!runs.length) return null;

  return (
    <div className="ops-server-active-banner-stack">
      {runs.map((run) => {
        const isRunning = run.status === "running";
        const title = operationActionLabel(run.action, t);
        const description = run.summary || t("servers.runningOp");

        return (
          <div key={run.id} className="ops-server-active-banner" data-tone="active">
            <div className="ops-server-active-banner__main">
              <span className="ops-server-active-banner__icon" data-spin="true">
                <IqujiIcon decorative name="loader" size={16} />
              </span>
              <div className="ops-server-active-banner__copy">
                <div className="ops-server-active-banner__headline">
                  <strong className="ops-server-active-banner__title">{title}</strong>
                  <IqujiBadge tone={isRunning ? "info" : "neutral"}>
                    {isRunning ? t("state.running") : t("state.pending")}
                  </IqujiBadge>
                </div>
                <p className="ops-server-active-banner__text">{description}</p>
              </div>
            </div>
            <div className="ops-server-active-banner__actions">
              <IqujiButton
                compact
                variant="secondary"
                disabled={disabled}
                onClick={() => onCancel(run)}
              >
                {t("servers.cancelOldOp")}
              </IqujiButton>
            </div>
          </div>
        );
      })}
    </div>
  );
}

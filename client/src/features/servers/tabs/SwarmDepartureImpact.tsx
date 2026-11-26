import { IqujiAlert, IqujiButton } from "@iquji-community/web-component";
import type { UseQueryResult } from "@tanstack/react-query";
import { errorMessage, operationErrorMessage, swarmImpactReasonMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import type { SwarmUninstallImpact } from "../api";

export function SwarmDepartureImpact({
  query,
  waitsForQueue,
  impactBlocked,
}: {
  query: UseQueryResult<SwarmUninstallImpact>;
  waitsForQueue: boolean;
  impactBlocked: boolean;
}) {
  const { t } = useI18n();
  return (
    <>
      {query.isFetching ? <p>{t("servers.checkingExitImpact")}</p> : null}
      {query.isError ? (
        <IqujiAlert title={t("servers.impactCheckFailed")} tone="danger">
          {errorMessage(query.error)}
          <IqujiButton compact variant="text" onClick={() => void query.refetch()}>
            {t("servers.recheck")}
          </IqujiButton>
        </IqujiAlert>
      ) : null}
      {query.data ? (
        <IqujiAlert
          title={
            waitsForQueue
              ? t("operations.waitingPredecessors", {
                  count: query.data.reasons.length,
                })
              : impactBlocked
                ? t("servers.resolveBlockersFirst")
                : t("servers.impactCheckPassed")
          }
          tone={impactBlocked ? "warning" : "info"}
        >
          {query.data.errorCode && !waitsForQueue ? (
            <p>{operationErrorMessage(query.data.errorCode)}</p>
          ) : null}
          {impactBlocked
            ? query.data.reasons.map((item) => (
                <p key={`${item.code}-${item.resourceKind}-${item.resourceId}-${item.field}`}>
                  {swarmImpactReasonMessage(item)}
                </p>
              ))
            : null}
        </IqujiAlert>
      ) : null}
    </>
  );
}

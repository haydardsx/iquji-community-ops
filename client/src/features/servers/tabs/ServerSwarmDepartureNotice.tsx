import { IqujiButton, IqujiField, IqujiSelect } from "@iquji-community/web-component";
import { useState } from "react";
import type { Server, ServerRuntimeMetrics } from "@/contracts/types";
import { operationErrorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { useOpenOperationLog } from "@/shared/ui/operation-log-drawer";
import { swarmMembership } from "../runtime-presentation";
import { ServerOperationLogDrawer } from "./ServerOperationLogDrawer";

export function ServerSwarmDepartureNotice({
  server,
  metrics,
}: {
  server: Server;
  metrics: ServerRuntimeMetrics;
}) {
  const { t, locale } = useI18n();
  const [open, setOpen] = useState(false);
  const openLog = useOpenOperationLog();
  const [selected, setSelected] = useState(0);
  const departures = (server.swarmDepartures ?? []).filter(
    (entry) => entry.cleanupStatus === "pending" && entry.departedAt.trim() !== "",
  );
  const selectedIndex = departures[selected] ? selected : 0;
  const departure = departures[selectedIndex];
  if (!departure || swarmMembership(metrics) !== "inactive") return null;
  const hasOperation = Boolean(departure.operationRunId);
  return (
    <>
      <span className="ops-muted ops-text-caption">
        {t("servers.departureOldNodesCount", { count: departures.length })}
        <IqujiButton
          compact
          variant="text"
          onClick={() =>
            departure.operationRunId ? openLog(departure.operationRunId) : setOpen(true)
          }
        >
          {hasOperation ? t("servers.viewDepartureLogs") : t("servers.viewServerLogs")}
        </IqujiButton>
      </span>
      {!hasOperation ? (
        <ServerOperationLogDrawer
          serverId={server.id}
          open={open}
          onClose={() => setOpen(false)}
          title={
            hasOperation ? t("servers.swarmDepartureLogTitle") : t("servers.serverRuntimeLogTitle")
          }
          description={t("servers.departureDesc")}
          headerMeta={
            <div className="ops-stack ops-text-caption" style={{ overflowWrap: "anywhere" }}>
              <span>
                {t("servers.originalNode")}{" "}
                <code>{departure.nodeId || t("servers.notRecorded")}</code> ·{" "}
                {t("servers.originalCluster")}{" "}
                <code>{departure.clusterId || t("servers.notRecorded")}</code>
              </span>
              <span>
                {t("servers.originalManagerEndpoint", {
                  endpoint: departure.managerAddress || t("servers.notRecorded"),
                })}
              </span>
              {departure.reasonCode ? (
                <span>
                  {departure.reasonCode === "SWARM_CLUSTER_MISMATCH"
                    ? t("servers.clusterMismatchError")
                    : operationErrorMessage(departure.reasonCode)}
                </span>
              ) : null}
            </div>
          }
          headerToolbar={
            departures.length > 1 ? (
              <IqujiField label={t("servers.departureRecords")}>
                <IqujiSelect
                  value={String(selectedIndex)}
                  options={departures.map((entry, index) => ({
                    value: String(index),
                    label: `${new Date(entry.departedAt).toLocaleString(locale)} · ${t("servers.pendingCleanup")}`,
                  }))}
                  onValueChange={(value) => setSelected(Number(value ?? 0))}
                />
              </IqujiField>
            ) : undefined
          }
        />
      ) : null}
    </>
  );
}

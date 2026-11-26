import { IqujiAlert } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import type { Server, ServerRuntimeMetrics } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import {
  getSwarmConnectionFacts,
  type SwarmConnectionMode,
  swarmConnectionModeLabel,
} from "../swarm-connection";
import { ServerSwarmRebuild } from "./ServerSwarmRebuild";
import "./ServerSwarmConnectionNotice.css";

export function ServerSwarmConnectionNotice({
  managers,
  showTips = true,
  showMismatchAlert = true,
  connectionMode,
  disabled,
  onManagerChanged,
}: {
  managers: Server[];
  showTips?: boolean;
  showMismatchAlert?: boolean;
  connectionMode?: SwarmConnectionMode;
  disabled?: boolean;
  onManagerChanged?: () => void;
}) {
  const { t } = useI18n();
  return (
    <div className="ops-swarm-notice">
      {managers.map((manager) => {
        const metrics = manager.health?.metrics as ServerRuntimeMetrics | undefined;
        const facts = getSwarmConnectionFacts({ ...metrics, swarmRole: "manager" }, t);
        const mismatch =
          connectionMode && facts.mode !== "unknown" && facts.mode !== connectionMode;
        return (
          <div className="ops-stack" key={manager.id}>
            <div className="ops-swarm-notice__manager-card">
              <span className="ops-swarm-notice__manager-icon">
                <IqujiIcon decorative name="network" size={15} />
              </span>
              <div className="ops-swarm-notice__manager-text">
                <strong>{manager.name}</strong> · {swarmConnectionModeLabel(facts.mode, t)} ·{" "}
                {metrics?.swarmManagerAddress
                  ? t("servers.actualManagerEndpoint", { endpoint: facts.endpoint })
                  : t("servers.probedAdvertiseIp", {
                      address: facts.address || t("swarm.statusUnverified"),
                    })}
              </div>
            </div>
            {showMismatchAlert && mismatch ? (
              <IqujiAlert title={t("servers.rebuildManagerNetworkTitle")} tone="warning">
                {t("servers.rebuildManagerNetworkDesc", {
                  current: swarmConnectionModeLabel(facts.mode, t),
                  target: swarmConnectionModeLabel(connectionMode, t),
                })}
              </IqujiAlert>
            ) : null}
            {onManagerChanged && mismatch ? (
              <ServerSwarmRebuild
                server={manager}
                disabled={disabled}
                initialConnectionMode={connectionMode}
                onChanged={onManagerChanged}
              />
            ) : null}
          </div>
        );
      })}
      {showTips ? (
        <div className="ops-swarm-notice__tips">
          <div className="ops-swarm-notice__tip-row">
            <span className="ops-swarm-notice__tip-icon">
              <IqujiIcon decorative name="circle-info" size={13} />
            </span>
            <span className="ops-swarm-notice__tip-text">{t("servers.swarmPortTips")}</span>
          </div>
        </div>
      ) : null}
    </div>
  );
}

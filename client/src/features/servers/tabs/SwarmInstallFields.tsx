import { IqujiField, IqujiSelect } from "@iquji-community/web-component";
import type { Server, ServerRuntimeMetrics } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { getSwarmConnectionFacts, type SwarmConnectionMode } from "../swarm-connection";
import { ServerSwarmConnectionNotice } from "./ServerSwarmConnectionNotice";
import { SwarmAddressFields } from "./SwarmAddressFields";

export function SwarmInstallFields({
  server,
  role,
  managers,
  managerId,
  connectionMode,
  advertiseAddr,
  disabled,
  keepExisting,
  onManagerChange,
  onConnectionModeChange,
  onAdvertiseAddrChange,
  onManagerChanged,
}: {
  server: Server;
  role: "manager" | "worker";
  managers: Server[];
  managerId: string;
  connectionMode: SwarmConnectionMode;
  advertiseAddr: string;
  disabled: boolean;
  keepExisting: boolean;
  onManagerChange: (id: string) => void;
  onConnectionModeChange: (mode: SwarmConnectionMode) => void;
  onAdvertiseAddrChange: (address: string) => void;
  onManagerChanged: () => void;
}) {
  const { t } = useI18n();
  const selectedManager = managers.find((manager) => manager.id === managerId);
  const nodeConnection = getSwarmConnectionFacts(
    (server.health?.metrics ?? {}) as ServerRuntimeMetrics,
    t,
  );
  const unverifiedExisting = keepExisting && nodeConnection.mode === "unknown";
  return (
    <>
      {role === "worker" ? (
        <IqujiField label={t("servers.targetManager")}>
          <IqujiSelect
            disabled={disabled || !managers.length}
            placeholder={managers.length ? undefined : t("servers.noAvailableManager")}
            value={managerId || null}
            options={managers.map((manager) => ({
              label: `${manager.name} · ${getSwarmConnectionFacts((manager.health?.metrics ?? {}) as ServerRuntimeMetrics, t).label}`,
              value: manager.id,
            }))}
            onValueChange={(value) => onManagerChange(value ?? "")}
          />
        </IqujiField>
      ) : null}
      {unverifiedExisting ? (
        <p className="ops-muted">{t("servers.addressUnverifiedRecheck")}</p>
      ) : (
        <SwarmAddressFields
          server={server}
          connectionMode={connectionMode}
          advertiseAddr={advertiseAddr}
          disabled={disabled}
          keepExisting={keepExisting}
          onConnectionModeChange={onConnectionModeChange}
          onAdvertiseAddrChange={onAdvertiseAddrChange}
        />
      )}
      <ServerSwarmConnectionNotice
        managers={role === "worker" && selectedManager ? [selectedManager] : []}
        connectionMode={unverifiedExisting ? undefined : connectionMode}
        disabled={disabled}
        onManagerChanged={onManagerChanged}
        showTips={false}
      />
    </>
  );
}

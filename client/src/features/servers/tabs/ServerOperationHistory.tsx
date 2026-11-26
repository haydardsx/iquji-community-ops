import { IqujiButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useState } from "react";
import type { OperationRun } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { OperationHistoryButton } from "@/shared/ui/operation-history";
import { swarmRebuildRecoveryOperationId } from "../swarm-rebuild-recovery";
import { ServerSwarmRebuildRecovery } from "./ServerSwarmRebuildRecovery";

export function ServerOperationHistory({ serverId }: { serverId: string }) {
  const { t } = useI18n();
  const [recovering, setRecovering] = useState<OperationRun | null>(null);
  return (
    <>
      <OperationHistoryButton
        scope={{ serverId, section: "server-runtime" }}
        renderActions={(run, rows, index) => {
          const originalId = swarmRebuildRecoveryOperationId(run);
          if (
            !originalId ||
            rows.findIndex(
              (candidate) => swarmRebuildRecoveryOperationId(candidate) === originalId,
            ) !== index
          )
            return null;
          return (
            <IqujiButton
              compact
              variant="secondary"
              iconStart={<IqujiIcon decorative name="refresh" />}
              onClick={() => setRecovering(run)}
            >
              {t("servers.continueRecovery")}
            </IqujiButton>
          );
        }}
      />
      {recovering ? (
        <ServerSwarmRebuildRecovery run={recovering} onClose={() => setRecovering(null)} />
      ) : null}
    </>
  );
}

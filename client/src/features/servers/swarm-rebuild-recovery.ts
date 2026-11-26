import type { OperationRun } from "@/contracts/types";

export function swarmRebuildRecoveryOperationId(run: OperationRun) {
  const recovery = run.details?.swarmRebuildRecovery;
  if (
    run.targetKind !== "server" ||
    !["rebuild_docker_swarm", "recover_docker_swarm_rebuild"].includes(run.action) ||
    recovery?.needsRecovery !== true
  )
    return undefined;
  return recovery.operationRunId?.trim() || undefined;
}

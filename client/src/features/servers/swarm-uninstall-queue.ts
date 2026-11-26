import type { SwarmUninstallImpact } from "./api";

/** 只有活动任务导致的阻断可转为等待；真实依赖仍须先解除。 */
export function swarmUninstallWaitsForTask(impact?: SwarmUninstallImpact) {
  return Boolean(
    impact?.blocked &&
      impact.errorCode === "SWARM_UNINSTALL_BLOCKED_ACTIVE_TASK" &&
      impact.reasons.length > 0 &&
      impact.reasons.every((reason) => reason.code === "SERVER_HAS_ACTIVE_TASK"),
  );
}

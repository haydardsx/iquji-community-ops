import { describe, expect, it } from "vitest";
import type { OperationRun } from "@/contracts/types";
import { swarmRebuildRecoveryOperationId } from "./swarm-rebuild-recovery";

const original: OperationRun = {
  id: "operation_original",
  action: "rebuild_docker_swarm",
  status: "failed",
  targetKind: "server",
  targetId: "server_manager",
  errorCode: "SWARM_REBUILD_ROLLBACK_FAILED",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:01:00Z",
};
const recovery = { needsRecovery: true, operationRunId: original.id };

describe("Swarm 恢复入口事实", () => {
  it("只依据服务端安全投影，不按失败错误码猜测备份存在", () => {
    expect(swarmRebuildRecoveryOperationId(original)).toBeUndefined();
    expect(
      swarmRebuildRecoveryOperationId({ ...original, details: { swarmRebuildRecovery: recovery } }),
    ).toBe(original.id);
  });

  it("失败恢复记录与取消记录都使用同一原始备份身份", () => {
    for (const run of [
      { ...original, id: "operation_recovery", action: "recover_docker_swarm_rebuild" },
      { ...original, status: "cancelled" },
    ])
      expect(
        swarmRebuildRecoveryOperationId({ ...run, details: { swarmRebuildRecovery: recovery } }),
      ).toBe(original.id);
  });

  it("服务端确认已恢复或正在恢复时不再提供重复入口", () => {
    expect(
      swarmRebuildRecoveryOperationId({
        ...original,
        details: { swarmRebuildRecovery: { ...recovery, needsRecovery: false } },
      }),
    ).toBeUndefined();
  });

  it("无备份身份或非 Swarm 维护记录不提供恢复入口", () => {
    for (const run of [
      { ...original, details: { swarmRebuildRecovery: { ...recovery, operationRunId: "" } } },
      { ...original, action: "install_docker_swarm", details: { swarmRebuildRecovery: recovery } },
      { ...original, targetKind: "application", details: { swarmRebuildRecovery: recovery } },
    ])
      expect(swarmRebuildRecoveryOperationId(run)).toBeUndefined();
  });
});

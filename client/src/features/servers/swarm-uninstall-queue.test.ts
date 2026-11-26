import { describe, expect, it } from "vitest";
import { swarmUninstallWaitsForTask } from "./swarm-uninstall-queue";

describe("Swarm 退出排队提示", () => {
  it("唯一活动任务影响可排队", () => {
    expect(
      swarmUninstallWaitsForTask({
        blocked: true,
        errorCode: "SWARM_UNINSTALL_BLOCKED_ACTIVE_TASK",
        reasons: [{ code: "SERVER_HAS_ACTIVE_TASK" }],
      }),
    ).toBe(true);
  });
  it("活动任务与真实运行引用同时存在时仍阻断", () => {
    expect(
      swarmUninstallWaitsForTask({
        blocked: true,
        errorCode: "SWARM_UNINSTALL_BLOCKED_ACTIVE_TASK",
        reasons: [{ code: "SERVER_HAS_ACTIVE_TASK" }, { code: "SERVER_HAS_APPLICATION_INSTANCE" }],
      }),
    ).toBe(false);
  });
  it("未知阻断与未完成影响检查不被视为可排队", () => {
    expect(swarmUninstallWaitsForTask()).toBe(false);
    expect(
      swarmUninstallWaitsForTask({
        blocked: true,
        errorCode: "SWARM_UNINSTALL_BLOCKED_ACTIVE_TASK",
        reasons: [],
      }),
    ).toBe(false);
  });
});

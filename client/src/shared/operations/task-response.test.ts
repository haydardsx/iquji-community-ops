import { describe, expect, it } from "vitest";

import { toOperationRun } from "./task-response";

const task = {
  id: "task_1",
  action: "start_frp_tunnel",
  status: "queued",
  target: { kind: "frp_tunnel_run", id: "tunrun_1", name: "开发回调" },
  createdAt: "2026-10-30T00:00:00Z",
  updatedAt: "2026-10-30T00:00:01Z",
};

describe("任务响应转操作运行", () => {
  it("保留动作、目标与状态", () => {
    expect(toOperationRun(task)).toMatchObject({
      id: "task_1",
      action: "start_frp_tunnel",
      status: "queued",
      targetKind: "frp_tunnel_run",
      targetId: "tunrun_1",
      targetName: "开发回调",
    });
  });

  it("缺少目标标识时拒绝转换", () => {
    expect(() => toOperationRun({ ...task, target: { kind: "frp_tunnel_run", id: "" } })).toThrow(
      "操作响应缺少标识",
    );
  });
});

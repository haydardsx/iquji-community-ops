import { describe, expect, it, vi } from "vitest";
import type { OperationActivityItem } from "./activity";
import { operationActivityChanges } from "./activity-state";
import { trackedOperationCompletion } from "./tracked-operation";

const active: OperationActivityItem = {
  id: "operation_example",
  action: "install_docker_engine",
  status: "running",
  targetKind: "server",
  targetId: "server_example",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  readVersion: "running:2026-01-01T00:00:00Z",
  active: true,
  unread: false,
  scope: {
    navigation: "servers",
    serverIds: ["server_example"],
  },
};
const terminal = {
  ...active,
  status: "succeeded",
  active: false,
  unread: true,
  readVersion: "succeeded:2026-01-01T00:01:00Z",
  updatedAt: "2026-01-01T00:01:00Z",
};

describe("活动终态被其他日志窗口提前读过", () => {
  it("活动直接消失也刷新原业务范围，终态已读移除不重复刷新", () => {
    expect(operationActivityChanges([active], [])).toEqual([active]);
    expect(operationActivityChanges([terminal], [])).toEqual([]);
    expect(operationActivityChanges([active], [terminal])).toEqual([terminal]);
  });

  it("新快照遗漏当前跟踪项时补查终态，正常活动任务不逐项轮询", async () => {
    const get = vi.fn().mockResolvedValue(terminal);
    await expect(
      trackedOperationCompletion(active.id, 1, { revision: 1, items: [] }, get),
    ).resolves.toBeUndefined();
    await expect(
      trackedOperationCompletion(active.id, 1, { revision: 2, items: [active] }, get),
    ).resolves.toBeUndefined();
    expect(get).not.toHaveBeenCalled();
    await expect(
      trackedOperationCompletion(active.id, 1, { revision: 3, items: [] }, get),
    ).resolves.toEqual(terminal);
    expect(get).toHaveBeenCalledTimes(1);
    expect(get).toHaveBeenCalledWith(active.id);
  });
});

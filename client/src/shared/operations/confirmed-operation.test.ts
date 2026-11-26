import { describe, expect, it, vi } from "vitest";
import type { OperationRun } from "@/contracts/types";
import { ApiError } from "@/shared/api/client";
import { ConfirmedOperationSession } from "./confirmed-operation";

const run: OperationRun = {
  id: "operation_one",
  action: "install_docker_engine",
  targetKind: "server",
  targetId: "server_one",
  status: "queued",
  createdAt: "2026-10-30T00:00:00Z",
  updatedAt: "2026-10-30T00:00:00Z",
};
const input = {
  action: run.action,
  targetKind: run.targetKind,
  targetId: run.targetId,
  label: "安装 Docker",
};
const apiError = (errorCode: string, details?: unknown) =>
  new ApiError({
    status: 409,
    body: { errorCode, details },
    fallbackMessage: errorCode,
  });
const makeApi = () => ({
  get: vi.fn().mockResolvedValue(run),
  confirm: vi.fn().mockResolvedValue(run),
  cancel: vi.fn().mockResolvedValue({ ...run, status: "cancelled" }),
});

describe("已确认操作提交", () => {
  it("确认调用前不会创建，创建后才确认", async () => {
    const api = makeApi();
    const session = new ConfirmedOperationSession(api);
    const create = vi.fn().mockResolvedValue(run);
    expect(create).not.toHaveBeenCalled();
    await session.submit({ ...input, create });
    expect(create).toHaveBeenCalledTimes(1);
    expect(api.confirm).toHaveBeenCalledWith(run.id);
  });

  it("确认网络失败保留 ID，重试不会重复创建", async () => {
    const api = makeApi();
    api.confirm.mockRejectedValueOnce(new Error("连接中断"));
    const session = new ConfirmedOperationSession(api);
    const create = vi.fn().mockResolvedValue(run);
    await expect(session.submit({ ...input, create })).rejects.toThrow("连接中断");
    expect(session.operationId).toBe(run.id);
    await session.submit({ ...input, create });
    expect(create).toHaveBeenCalledTimes(1);
    expect(api.confirm).toHaveBeenCalledTimes(2);
  });

  it("重复确认查询真实终态，目标不一致时不确认", async () => {
    const api = makeApi();
    api.confirm.mockRejectedValueOnce(apiError("TASK_NOT_CONFIRMABLE"));
    api.get.mockResolvedValueOnce({ ...run, status: "succeeded" });
    const session = new ConfirmedOperationSession(api);
    expect((await session.submit({ ...input, create: async () => run })).status).toBe("succeeded");
    await expect(
      session.submit({ ...input, create: async () => ({ ...run, targetId: "another" }) }),
    ).rejects.toThrow("目标不一致");
    expect(api.confirm).toHaveBeenCalledTimes(1);
  });

  it("创建响应丢失后沿用同一请求 key，明确再次提交创建新 key", async () => {
    const api = makeApi();
    const makeKey = vi
      .fn()
      .mockReturnValueOnce("request_first")
      .mockReturnValueOnce("request_second");
    const session = new ConfirmedOperationSession(api, makeKey);
    const create = vi.fn().mockRejectedValueOnce(new Error("连接中断")).mockResolvedValue(run);
    await expect(session.submit({ ...input, create })).rejects.toThrow("连接中断");
    await session.submit({ ...input, create });
    expect(create.mock.calls[0][0]).toBe("request_first");
    expect(create.mock.calls[1][0]).toBe("request_first");
    await session.submit({ ...input, create });
    expect(create.mock.calls[2][0]).toBe("request_second");
  });

  it("存在未消歧的创建请求时不能切换目标，重置后可明确提交新请求", async () => {
    const api = makeApi();
    const session = new ConfirmedOperationSession(api);
    const create = vi.fn().mockRejectedValueOnce(new Error("连接中断")).mockResolvedValue(run);
    await expect(session.submit({ ...input, create })).rejects.toThrow("连接中断");
    await expect(session.submit({ ...input, targetId: "server_other", create })).rejects.toThrow(
      "切换目标",
    );
    const firstKey = create.mock.calls[0][0];
    session.reset();
    await session.submit({ ...input, create });
    expect(create.mock.calls[1][0]).not.toBe(firstKey);
  });

  it("服务器报告资源忙碌时不自动确认其他请求的操作", async () => {
    const api = makeApi();
    const session = new ConfirmedOperationSession(api);
    await expect(
      session.submit({
        ...input,
        create: async () => {
          throw apiError("TASK_ALREADY_ACTIVE", { activeTaskId: run.id });
        },
      }),
    ).rejects.toBeInstanceOf(ApiError);
    expect(api.get).not.toHaveBeenCalled();
    expect(api.confirm).not.toHaveBeenCalled();
  });
});

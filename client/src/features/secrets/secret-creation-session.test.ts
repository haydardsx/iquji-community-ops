import { describe, expect, it, vi } from "vitest";
import type { OperationRun, SecretAsset } from "@/contracts/types";
import { ConfirmedOperationSession } from "@/shared/operations/confirmed-operation";
import { SecretCreationSession } from "./secret-creation-session";

const asset: SecretAsset = {
  id: "secret_one",
  assetType: "ssh_key",
  name: "部署密钥",
  createdAt: "2026-11-16T00:00:00Z",
  updatedAt: "2026-11-16T00:00:00Z",
};
const run: OperationRun = {
  id: "operation_one",
  action: "create_secret_asset",
  targetKind: "secret_asset",
  targetId: asset.id,
  status: "queued",
  createdAt: asset.createdAt,
  updatedAt: asset.updatedAt,
};

const setup = () => {
  const api = {
    get: vi.fn().mockResolvedValue(run),
    confirm: vi.fn().mockResolvedValue(run),
    cancel: vi.fn().mockResolvedValue({ ...run, status: "cancelled" }),
  };
  const operation = new ConfirmedOperationSession(api);
  const create = vi.fn().mockResolvedValue({ asset, operation: run });
  const input = {
    label: "保存部署密钥",
    create,
    submit: operation.submit.bind(operation),
  };
  return { api, create, input, session: new SecretCreationSession() };
};

describe("新建凭据确认入队", () => {
  it("确认网络失败后重试沿用原凭据操作", async () => {
    const { api, create, input, session } = setup();
    api.confirm.mockRejectedValueOnce(new Error("确认连接中断"));
    await expect(session.save(input)).rejects.toThrow("确认连接中断");
    expect(session.hasAsset).toBe(true);
    await expect(session.save(input)).resolves.toBe(true);
    expect(create).toHaveBeenCalledTimes(1);
    expect(api.confirm).toHaveBeenCalledTimes(2);
    expect(create.mock.calls[0][0]).toEqual(expect.any(String));
  });

  it("确认入队后不再重复创建，绑定与材料生效由 Worker 推进", async () => {
    const { api, create, input, session } = setup();
    await expect(session.save(input)).resolves.toBe(true);
    await expect(session.save(input)).resolves.toBe(true);
    expect(create).toHaveBeenCalledTimes(1);
    expect(api.confirm).toHaveBeenCalledTimes(1);
  });

  it.each(["failed", "cancelled"])("终态 %s 不返回成功", async (status) => {
    const { api, create, input, session } = setup();
    api.confirm.mockResolvedValueOnce({ ...run, status });
    await expect(session.save(input)).resolves.toBe(false);
    expect(session.rejected).toBe(true);
    await expect(session.save(input)).resolves.toBe(false);
    expect(create).toHaveBeenCalledTimes(1);
  });

  it("缺少操作记录时保留资产且拒绝假成功", async () => {
    const { create, input, session } = setup();
    create.mockResolvedValue({ asset });
    await expect(session.save(input)).rejects.toThrow("未返回操作记录");
    expect(session.hasAsset).toBe(true);
    await expect(session.save(input)).rejects.toThrow("未返回操作记录");
    expect(create).toHaveBeenCalledTimes(1);
  });
});

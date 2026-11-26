import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchJson } from "@/shared/api/client";
import { privateIPv4Boundary } from "@/test/swarm-addresses";
import {
  fetchServerOperationLogs,
  installServerComponent,
  rebuildSwarm,
  recoverSwarmRebuild,
} from "./api";

vi.mock("@/shared/api/client", () => ({ fetchJson: vi.fn() }));

beforeEach(() => vi.resetAllMocks());

describe("服务器 Swarm 与诊断请求", () => {
  it("加入集群保留目标 manager 与节点通告地址", async () => {
    const input = {
      mode: "worker" as const,
      managerServerId: "server_manager",
      advertiseAddr: "192.0.2.10",
      connectionMode: "public" as const,
    };
    await installServerComponent("server_worker", "swarm", input);
    expect(fetchJson).toHaveBeenCalledWith("servers/server_worker/swarm/install", {
      method: "POST",
      body: input,
    });
  });

  it.each([
    ["public", "192.0.2.20"],
    ["private", privateIPv4Boundary],
  ] as const)("备份重建保留 %s 目标通信网络与地址", async (connectionMode, advertiseAddr) => {
    const input = { connectionMode, advertiseAddr };
    await rebuildSwarm("server_manager", input);
    expect(fetchJson).toHaveBeenCalledWith("servers/server_manager/swarm/rebuild", {
      method: "POST",
      body: input,
    });
  });

  it("继续恢复引用原备份操作并创建独立确认任务", async () => {
    await recoverSwarmRebuild("server_manager", "operation_original");
    expect(fetchJson).toHaveBeenCalledWith("servers/server_manager/swarm/rebuild/recover", {
      method: "POST",
      body: { operationRunId: "operation_original" },
    });
  });

  it("诊断日志同时限定服务器与操作并编码查询参数", async () => {
    await fetchServerOperationLogs("server/a", "operation&one");
    expect(fetchJson).toHaveBeenCalledWith(
      "logs/runtime?targetKind=server&targetId=server%2Fa&operationRunId=operation%26one&page=1&pageSize=100&order=desc",
    );
  });

  it("缺少原任务 ID 时提供真实服务器日志，不拼造脱离任务身份", async () => {
    await fetchServerOperationLogs("server/a");
    expect(fetchJson).toHaveBeenCalledWith(
      "logs/runtime?targetKind=server&targetId=server%2Fa&page=1&pageSize=100&order=desc",
    );
  });
});

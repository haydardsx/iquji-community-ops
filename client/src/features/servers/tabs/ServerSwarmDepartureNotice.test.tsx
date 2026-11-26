import { IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { Server, ServerRuntimeMetrics, SwarmDeparture } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { ServerSwarmDepartureNotice } from "./ServerSwarmDepartureNotice";

const departure: SwarmDeparture = {
  nodeId: "node_previous",
  clusterId: "cluster_previous",
  managerServerId: "server_previous_manager",
  managerAddress: "192.0.2.20:2377",
  cleanupStatus: "pending",
  reasonCode: "SWARM_MANAGER_SSH_UNAVAILABLE",
  departedAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  operationRunId: "operation_departure",
};

function renderNotice(
  departures: SwarmDeparture[],
  metrics: ServerRuntimeMetrics = { swarm: false, swarmLocalNodeState: "inactive" },
) {
  const server: Server = {
    id: "server_worker",
    name: "示例节点",
    publicHost: "192.0.2.10",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    swarmDepartures: departures,
  };
  return renderToStaticMarkup(
    <QueryClientProvider client={new QueryClient()}>
      <IqujiOverlayProvider>
        <ServerSwarmDepartureNotice server={server} metrics={metrics} />
      </IqujiOverlayProvider>
    </QueryClientProvider>,
  );
}

describe("Swarm 已脱离待清理事实", () => {
  it("只展示已核验本机退出的待清理记录，原身份和细节收进日志抽屉", () => {
    const markup = renderNotice([
      departure,
      { ...departure, nodeId: "node_other", clusterId: "cluster_other" },
      { ...departure, cleanupStatus: "removed" },
      { ...departure, cleanupStatus: "leaving", departedAt: "" },
      { ...departure, cleanupStatus: "leave_failed", departedAt: "" },
    ]);
    expect(markup).toContain("本机已脱离，原集群有 2 条旧节点记录待管理员清理");
    expect(markup).toContain("查看脱离日志");
    expect(markup).not.toContain(departure.nodeId);
    expect(markup).not.toContain(departure.clusterId);
    expect(markup).not.toContain(departure.managerAddress);
    expect(markup).not.toContain("未就绪");
  });

  it("未核验退出、已清理或本机正在入群/已入群时不宣称已脱离", () => {
    expect(renderNotice([{ ...departure, departedAt: "" }])).toBe("");
    expect(renderNotice([{ ...departure, cleanupStatus: "removed" }])).toBe("");
    for (const metrics of [
      {},
      { swarm: true, swarmRole: "worker" },
      { swarm: false, swarmRole: "none", swarmLocalNodeState: "pending" },
    ])
      expect(renderNotice([departure], metrics)).toBe("");
  });

  it("没有正式原任务 ID 时只提供服务器日志入口", () => {
    const markup = renderNotice([{ ...departure, operationRunId: "" }]);
    expect(markup).toContain("查看服务器日志");
    expect(markup).not.toContain("查看脱离日志");
  });
});

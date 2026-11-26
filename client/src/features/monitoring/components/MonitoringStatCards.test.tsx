import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { calculateSystemHealth } from "../monitoring-utils";
import { MonitoringStatCards } from "./MonitoringStatCards";

vi.mock("@/shared/operations/activity", () => ({ OperationActivityDot: () => null }));

describe("监控概览统计", () => {
  it("一个探针在线时，两台待核验主机不计为在线或离线", () => {
    const statuses = ["online", "unknown", "unknown"] as const;
    const servers = statuses.map((connectionStatus, index) => ({
      serverId: String(index),
      serverName: String(index),
      connectionStatus,
      agentStatus: index === 0 ? "online" : "not_installed",
      alerts: [],
    }));
    const summary = calculateSystemHealth([], servers, { status: "error", clusterCount: 0 }, [
      { id: "agent", serverId: "0", status: "online" },
    ]);
    const markup = renderToStaticMarkup(
      <MonitoringStatCards activeTab="servers" summary={summary} onSelectTab={() => {}} />,
    );
    expect(summary.onlineServers).toBe(1);
    expect(summary.totalServers).toBe(3);
    expect(summary.level).toBe("warning");
    expect(summary.swarmClusterCount).toBeNull();
    expect(markup).toContain("2 台主机待核验 · 0 台离线 · 探针 1/1 在线");
    expect(markup).not.toContain("主机全部在线");
    expect(calculateSystemHealth([], [], { status: "not_enabled" }).swarmClusterCount).toBe(0);
  });
});

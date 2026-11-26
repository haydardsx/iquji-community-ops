import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { MonitoringSwarmView } from "./MonitoringSwarmView";

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));

describe("首页 Swarm 节点状态", () => {
  it.each([
    ["Ready", "Active", "Leader"],
    ["ready", "active", "leader"],
  ])("将 Docker 的 %s/%s/%s 显示为在线管理节点", (status, availability, managerStatus) => {
    const markup = renderToStaticMarkup(
      <MonitoringSwarmView
        isLoading={false}
        swarm={{
          status: "ready",
          nodes: [
            {
              id: "node_example",
              role: "manager",
              sampleStatus: "fresh",
              status,
              availability,
              managerStatus,
            },
          ],
        }}
      />,
    );
    expect(markup).toContain("正常运行");
    expect(markup).not.toContain("节点离线");
    expect(markup).toContain("⭐ Leader");
    expect(markup).toMatch(/ops-node-status-cell.*?data-tone="success"/);
  });

  it.each(["unknown", undefined])("%s 节点状态保持未知语义", (status) => {
    const markup = renderToStaticMarkup(
      <MonitoringSwarmView
        isLoading={false}
        swarm={{ nodes: [{ id: "node_example", role: "worker", sampleStatus: "fresh", status }] }}
      />,
    );
    expect(markup).toContain("未知");
    expect(markup).not.toContain("节点离线");
    expect(markup).not.toContain("正常运行");
    expect(markup).toMatch(/ops-node-status-cell.*?data-tone="neutral"/);
  });

  it.each([
    "stale",
    "unknown",
    undefined,
  ])("缺少或失效的新鲜度 %s 不会把旧 Ready 当作在线、旧地址当作当前端点", (sampleStatus) => {
    const markup = renderToStaticMarkup(
      <MonitoringSwarmView
        isLoading={false}
        swarm={{
          status: "stale",
          nodes: [
            {
              id: "node_example",
              role: "manager",
              status: "Ready",
              managerStatus: "Leader",
              availability: "Active",
              sampleStatus,
              managerAddress: "192.0.2.24:3377",
            },
          ],
        }}
      />,
    );
    expect(markup).toContain(sampleStatus === "stale" ? "数据过期" : "待核验");
    expect(markup).not.toContain("192.0.2.24:3377");
    expect(markup).not.toContain("正常运行");
    expect(markup).not.toContain("⭐ Leader");
  });

  it("地址前缀或同名主机不会把外部节点链接到另一台服务器", () => {
    const markup = renderToStaticMarkup(
      <MonitoringSwarmView
        isLoading={false}
        serverList={[
          {
            serverId: "server_example",
            serverName: "example-node",
            publicHost: "192.0.2.1",
            agentStatus: "online",
            alerts: [],
          },
        ]}
        swarm={{
          status: "ready",
          nodes: [
            {
              id: "other_node",
              role: "manager",
              hostname: "example-node",
              advertiseAddr: "192.0.2.10",
            },
          ],
        }}
      />,
    );
    expect(markup).not.toContain("ops-node-link-btn");
    expect(markup).not.toContain("ops-swarm-tree-connector");
  });

  it("无可用样本时区分未启用与采集失败", () => {
    const markup = renderToStaticMarkup(
      <MonitoringSwarmView isLoading={false} swarm={{ status: "error" }} />,
    );
    expect(markup).toContain("采集失败");
    expect(markup).not.toContain("前往服务器");
  });

  it("显示管理节点返回的采集错误，避免将空拓扑解释为未启用", () => {
    const markup = renderToStaticMarkup(
      <MonitoringSwarmView
        isLoading={false}
        serverList={[
          { serverId: "manager", serverName: "示例管理节点", agentStatus: "online", alerts: [] },
        ]}
        swarm={{
          status: "error",
          snapshots: [
            {
              serverId: "manager",
              role: "manager",
              status: "error",
              errorCode: "SWARM_SNAPSHOT_INCOMPLETE",
            },
          ],
        }}
      />,
    );
    expect(markup).toContain("示例管理节点");
    expect(markup).toContain("未能完整读取管理节点的集群资源");
    expect(markup).toContain("SWARM_SNAPSHOT_INCOMPLETE");
    expect(markup).not.toContain("Swarm 未启用");
  });

  it("明确 down 的节点继续显示离线", () => {
    const markup = renderToStaticMarkup(
      <MonitoringSwarmView
        isLoading={false}
        swarm={{
          nodes: [{ id: "node_example", role: "worker", sampleStatus: "fresh", status: "down" }],
        }}
      />,
    );
    expect(markup).toContain("节点离线");
    expect(markup).not.toContain("正常运行");
    expect(markup).toMatch(/ops-node-status-cell.*?data-tone="warning"/);
  });

  it("保留其他节点的可用拓扑，并显示失败快照自己的来源", () => {
    const markup = renderToStaticMarkup(
      <MonitoringSwarmView
        isLoading={false}
        swarm={{
          status: "ready",
          services: [{ id: "example-service", name: "示例服务" }],
          snapshots: [
            {
              serverName: "示例主机",
              status: "error",
              errorCode: "SWARM_SNAPSHOT_INVALID",
            },
          ],
        }}
      />,
    );
    expect(markup).toContain("示例服务");
    expect(markup).toContain("示例主机");
    expect(markup).toContain("SWARM_SNAPSHOT_INVALID");
  });

  it("同时显示节点、服务与网络", () => {
    const markup = renderToStaticMarkup(
      <MonitoringSwarmView
        isLoading={false}
        swarm={{
          status: "ready",
          nodes: [{ id: "node-1", hostname: "host-1", role: "manager" }],
          services: [{ id: "srv-1", name: "app-service", image: "nginx:latest" }],
          networks: [{ id: "net-1", name: "app-net", driver: "overlay", scope: "swarm" }],
        }}
      />,
    );
    expect(markup).toContain("host-1");
    expect(markup).toContain("app-service");
    expect(markup).toContain("app-net");
  });

  it("管理节点排在工作节点之前，并显示工作节点的树形标记", () => {
    const markup = renderToStaticMarkup(
      <MonitoringSwarmView
        isLoading={false}
        swarm={{
          status: "ready",
          nodes: [
            {
              id: "worker-node-1",
              serverId: "srv-build",
              hostname: "worker.example.test",
              sampleStatus: "fresh",
              role: "worker",
              status: "ready",
              availability: "active",
            },
            {
              id: "manager-leader-node",
              serverId: "srv-gw",
              hostname: "manager.example.test",
              sampleStatus: "fresh",
              role: "manager",
              managerStatus: "leader",
              status: "ready",
              availability: "active",
            },
          ],
        }}
        serverList={[
          {
            serverId: "srv-gw",
            serverName: "网关服务器",
            publicHost: "192.0.2.11",
            status: "online",
            agentStatus: "healthy",
            labels: [],
            alerts: [],
            hostname: "manager.example.test",
          } as unknown as import("../api").MonitoringServer,
          {
            serverId: "srv-build",
            serverName: "环境构建服务器",
            publicHost: "192.0.2.10",
            status: "online",
            agentStatus: "healthy",
            labels: [],
            alerts: [],
            hostname: "worker.example.test",
          } as unknown as import("../api").MonitoringServer,
        ]}
      />,
    );

    const managerIndex = markup.indexOf("网关服务器");
    const workerIndex = markup.indexOf("环境构建服务器");
    expect(managerIndex).toBeGreaterThan(-1);
    expect(workerIndex).toBeGreaterThan(-1);
    expect(managerIndex).toBeLessThan(workerIndex);

    expect(markup).toContain("ops-swarm-tree-connector");
    expect(markup).toContain("└─");
  });
});

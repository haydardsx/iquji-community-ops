import { describe, expect, it } from "vitest";
import type { MonitoringAgent, MonitoringProject, MonitoringServer } from "./api";
import {
  agentStatusLabel,
  calculateSystemHealth,
  formatBytes,
  formatPercent,
  formatSampleTime,
  formatTimeAgo,
  getResourceTone,
} from "./monitoring-utils";

describe("monitoring-utils", () => {
  it("应准确汇总全健康系统状态", () => {
    const projects: MonitoringProject[] = [
      {
        projectId: "p1",
        projectName: "项目一",
        applicationId: "a1",
        applicationName: "应用一",
        status: "running",
        runningContainers: 2,
        alerts: [],
      },
    ];
    const servers: MonitoringServer[] = [
      {
        serverId: "s1",
        serverName: "服务器一",
        agentStatus: "online",
        connectionStatus: "online",
        alerts: [],
      },
    ];
    const agents: MonitoringAgent[] = [
      {
        id: "ag1",
        serverId: "s1",
        status: "online",
      },
    ];

    const result = calculateSystemHealth(
      projects,
      servers,
      { nodes: [{ id: "n1", clusterId: "c1", role: "manager" }], status: "ready" },
      agents,
    );
    expect(result.level).toBe("healthy");
    expect(result.serverOnlinePercent).toBe(100);
    expect(result.runningProjects).toBe(1);
    expect(result.offlineServers).toBe(0);
    expect(result.title).toContain("正常");
  });

  it("当存在离线主机时应输出警告级别", () => {
    const servers: MonitoringServer[] = [
      {
        serverId: "s1",
        serverName: "srv-1",
        agentStatus: "online",
        connectionStatus: "online",
        alerts: [],
      },
      {
        serverId: "s2",
        serverName: "srv-2",
        agentStatus: "offline",
        connectionStatus: "offline",
        alerts: [],
      },
      {
        serverId: "s3",
        serverName: "srv-3",
        agentStatus: "offline",
        connectionStatus: "offline",
        alerts: [],
      },
    ];
    const result = calculateSystemHealth([], servers, {}, []);
    expect(result.level).toBe("warning");
    expect(result.offlineServers).toBe(2);
    expect(result.serverOnlinePercent).toBe(33);
    expect(result.title).toContain("2 台主机离线");
  });

  it("当存在微服务异常或告警时应输出故障级别", () => {
    const projects: MonitoringProject[] = [
      {
        projectId: "p1",
        projectName: "项目",
        applicationId: "a1",
        applicationName: "应用",
        status: "danger",
        runningContainers: 0,
        alerts: [{ level: "critical", summary: "OOM" }],
      },
    ];
    const result = calculateSystemHealth(projects, [], {}, []);
    expect(result.level).toBe("critical");
    expect(result.errorProjects).toBe(1);
    expect(result.totalAlerts).toBe(1);
  });

  it("应正确格式化百分比与资源利用率级别", () => {
    expect(formatPercent(null)).toBe("--");
    expect(formatPercent(45.6)).toBe("46%");
    expect(formatPercent(0)).toBe("0%");

    expect(getResourceTone(null)).toBe("neutral");
    expect(getResourceTone(30)).toBe("success");
    expect(getResourceTone(75)).toBe("warning");
    expect(getResourceTone(92)).toBe("danger");
  });

  it("应正确格式化人性化时间与字节", () => {
    expect(formatTimeAgo(null)).toBe("未采样");
    const nowIso = new Date().toISOString();
    expect(formatTimeAgo(nowIso)).toBe("刚刚");

    expect(formatBytes(null)).toBe("--");
    expect(formatBytes(0)).toBe("--");
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(1024 * 1024 * 50)).toBe("50.0 MB");
  });
});

it("集群按真实 ID 计数，未安装探针不计入离线", () => {
  const result = calculateSystemHealth(
    [],
    [
      {
        serverId: "a",
        serverName: "A",
        agentStatus: "not_installed",
        connectionStatus: "online",
        alerts: [],
      },
    ],
    {
      clusterCount: 1,
      nodes: [
        { id: "a", clusterId: "cluster", role: "manager" },
        { id: "b", clusterId: "cluster", role: "worker" },
      ],
      status: "ready",
    },
    [{ id: "agent", serverId: "a", status: "not_installed" }],
  );
  expect(result.swarmClusterCount).toBe(1);
  expect(result.swarmNodeCount).toBe(2);
  expect(result.swarmManagerCount).toBe(1);
  expect(result.swarmWorkerCount).toBe(1);
  expect(result.totalAgents).toBe(0);
  expect(result.offlineServers).toBe(0);
});

it("探针状态和采样时间明确反映过期及本地时区", () => {
  expect(agentStatusLabel("stale")).toBe("心跳已过期");
  expect(agentStatusLabel("not_installed")).toBe("未安装探针");
  expect(formatSampleTime("2026-11-16T11:10:37Z", "zh-CN", "Asia/Shanghai")).toContain("19:10:37");
  expect(formatSampleTime("invalid")).toBe("—");
});

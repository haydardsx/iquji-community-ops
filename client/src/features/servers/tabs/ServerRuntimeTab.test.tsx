import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { OperationRun, Server, ServerRuntimeMetrics } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { ServerRuntimeFacts } from "./ServerRuntimeFacts";
import { ServerRuntimeTab } from "./ServerRuntimeTab";

vi.mock("@/shared/operations/use-confirmed-operation", () => ({
  useConfirmedOperation: () => ({ isPending: false }),
}));
vi.mock("./ServerGatewaySection", () => ({ ServerGatewaySection: () => null }));

const server: Server = {
  id: "server_worker",
  name: "示例节点",
  publicHost: "192.0.2.10",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

function renderRuntime(
  swarmLocalNodeState: string,
  runs: OperationRun[] = [],
  extraMetrics: ServerRuntimeMetrics = {},
  extraServer: Partial<Server> = {},
) {
  const client = new QueryClient();
  client.setQueryData(["servers"], { items: [] });
  client.setQueryData(["operation-runs", "server", server.id], { items: runs });
  client.setQueryData(["operation-runs", "server", server.id, "failed"], []);
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <IqujiOverlayProvider>
        <IqujiNotificationProvider closeLabel="关闭通知">
          <ServerRuntimeTab
            server={{ ...server, ...extraServer }}
            metrics={{
              ssh: true,
              docker: true,
              swarm: false,
              swarmLocalNodeState,
              ...extraMetrics,
            }}
            onRefresh={() => {}}
          />
        </IqujiNotificationProvider>
      </IqujiOverlayProvider>
    </QueryClientProvider>,
  );
}

describe("Swarm 未完成加入恢复操作", () => {
  it.each(["pending", "error"])("%s 状态提供退出确认入口", (state) => {
    const markup = renderRuntime(state);
    expect(markup).toContain("退出未完成加入");
    expect(markup).not.toContain(">加入现有集群<");
    expect(markup).not.toContain(">创建新集群<");
  });

  it("身份未知时不提供创建、入群或离群操作", () => {
    const markup = renderRuntime("unknown", [], {
      swarmRole: "unknown",
      swarmVerificationStatus: "stale",
    });
    for (const label of [
      "加入现有集群",
      "创建新集群",
      "解散集群",
      "脱离集群",
      "备份并重建 Swarm",
    ]) {
      expect(markup).not.toContain(label);
    }
  });

  it.each([
    { swarmVerificationStatus: "stale" },
    { swarmVerificationStatus: "unverified" },
    { swarmVerificationStatus: "verified", swarmVerificationError: "SWARM_PROBE_FAILED" },
  ] as const)("身份或核验结果失效时隐藏旧控制权和地址", (state) => {
    const metrics = {
      ssh: true,
      docker: true,
      swarm: true,
      swarmRole: "manager",
      swarmControlAvailable: true,
      swarmManagerAddress: "192.0.2.41:2377",
      ...state,
    };
    const facts = renderToStaticMarkup(<ServerRuntimeFacts metrics={metrics} server={server} />);
    expect(facts).not.toContain("manager 控制权可用");
    expect(facts).toContain("待核验");
    const runtime = renderRuntime("active", [], metrics);
    expect(runtime).not.toContain(metrics.swarmManagerAddress);
    expect(runtime).not.toContain("备份并重建 Swarm");
  });

  it("详情区分明确缺失、Docker 暂不可用和 SSH 失败", () => {
    const facts = (metrics: ServerRuntimeMetrics) =>
      renderToStaticMarkup(<ServerRuntimeFacts metrics={metrics} server={server} />);
    const removed = facts({
      ssh: true,
      docker: false,
      dockerStatus: "missing",
      swarmVerificationStatus: "not_joined",
    });
    expect(removed).toContain("未安装");
    expect(removed).toContain("未启用");
    const unavailable = facts({
      ssh: true,
      docker: false,
      dockerStatus: "unavailable",
      swarmVerificationStatus: "stale",
    });
    expect(unavailable).toContain("暂不可用");
    expect(unavailable).not.toContain("未安装");
    expect(unavailable).not.toContain("未启用");
    const failed = facts({ ssh: false, docker: false, agent: false, swarmRole: "none" });
    expect(failed).toContain("状态待核验");
    expect(failed).not.toContain("未安装");
    expect(failed).not.toContain("未启用");
  });

  it("节点未入群时提供加入与创建入口", () => {
    const markup = renderRuntime("inactive");
    expect(markup).toContain("加入现有集群");
    expect(markup).toContain("创建新集群");
    expect(markup).not.toContain("退出未完成加入");
  });

  it("manager 的成员关系操作为解散集群", () => {
    const client = new QueryClient();
    client.setQueryData(["servers"], { items: [] });
    client.setQueryData(["operation-runs", "server", server.id], { items: [] });
    client.setQueryData(["operation-runs", "server", server.id, "failed"], []);
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <IqujiNotificationProvider closeLabel="关闭通知">
            <ServerRuntimeTab
              server={server}
              metrics={{
                ssh: true,
                docker: true,
                swarm: true,
                swarmRole: "manager",
                swarmVerificationStatus: "verified",
              }}
              onRefresh={() => {}}
            />
          </IqujiNotificationProvider>
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );
    const swarmRow = markup.split("Swarm 集群成员关系")[1]?.split('class="ops-service-row"')[0];
    expect(swarmRow).toContain("解散集群");
    expect(swarmRow).not.toContain("卸载组件");
  });

  it("worker 未关联 manager、保留 Jenkins 或有旧集群清理历史时仍提供脱离入口", () => {
    const markup = renderRuntime(
      "active",
      [],
      {
        swarm: true,
        swarmRole: "worker",
        swarmVerificationStatus: "unverified",
        jenkins: true,
      },
      {
        swarmDepartures: [
          {
            nodeId: "node_old",
            clusterId: "cluster_old",
            managerServerId: "server_old_manager",
            managerAddress: "192.0.2.20:2377",
            cleanupStatus: "pending",
            reasonCode: "SWARM_MANAGER_SSH_UNAVAILABLE",
            departedAt: "2026-01-01T00:00:00Z",
            updatedAt: "2026-01-01T00:00:00Z",
            operationRunId: "operation_old_leave",
          },
        ],
      },
    );
    const buttons = [...markup.matchAll(/<button([^>]*)>(.*?)<\/button>/g)];
    const leave = buttons.find((item) => item[2].includes("脱离集群"));
    expect(leave).toBeDefined();
    expect(leave?.[1]).not.toContain('disabled=""');
    const swarmRow = markup.split("Swarm 集群成员关系")[1]?.split('class="ops-service-row"')[0];
    expect(swarmRow).not.toContain("卸载组件");
    expect(swarmRow).not.toContain("本机已脱离");
    expect(markup).not.toContain("cluster_old");
  });

  it("manager 显示真实通信端点和备份重建入口", () => {
    const markup = renderRuntime("active", [], {
      swarm: true,
      swarmRole: "manager",
      swarmControlAvailable: true,
      swarmVerificationStatus: "verified",
      swarmManagerAddress: "192.0.2.10:3377",
    });
    expect(markup).toContain("备份并重建 Swarm");
    expect(markup).not.toContain("核验当前连接");
    expect(markup).toContain("公网 · 192.0.2.10:3377");
  });

  it("worker 节点已核验时提供「重新核验」入口，未核验时提供「关联并核验」入口", () => {
    const verifiedMarkup = renderRuntime("active", [], {
      swarm: true,
      swarmRole: "worker",
      swarmVerificationStatus: "verified",
    });
    expect(verifiedMarkup).toContain("重新核验");
    expect(verifiedMarkup).not.toContain("关联并核验");
    expect(verifiedMarkup).toContain("卸载组件");

    const unverifiedMarkup = renderRuntime("active", [], {
      swarm: true,
      swarmRole: "worker",
      swarmVerificationStatus: "unverified",
    });
    expect(unverifiedMarkup).toContain("关联并核验");
    expect(unverifiedMarkup).not.toContain("重新核验");
  });

  it.each([
    ["install_jenkins", false],
    ["install_monitoring_agent", false],
    ["install_docker_engine", false],
    ["uninstall_docker_swarm", false],
    ["reconfigure_docker_swarm", false],
    ["rebuild_docker_swarm", false],
    ["recover_docker_swarm_rebuild", false],
    ["install_traefik_gateway", false],
  ])("活动 %s 不阻止后续操作进入确认与队列", (action, blocked) => {
    const markup = renderRuntime("inactive", [
      {
        id: "operation_example",
        targetKind: "server",
        targetId: server.id,
        action,
        status: "running",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:01:00Z",
      },
    ]);
    const buttons = [...markup.matchAll(/<button([^>]*)>(.*?)<\/button>/g)];
    for (const label of ["加入现有集群", "创建新集群"]) {
      const button = buttons.find((item) => item[2].includes(label));
      expect(button).toBeDefined();
      expect(button?.[1].includes('disabled=""')).toBe(blocked);
    }
  });
});

describe("FRPS 运行环境", () => {
  it("已安装但未开启隧道时提示卸载", () => {
    const markup = renderRuntime("active", [], {
      frps: true,
      frpsManagedPresent: true,
      frpsIdle: true,
      frpsStatus: "installed",
    });
    const row = markup.split("FRPS 穿透服务")[1]?.split('class="ops-service-row"')[0];
    expect(row).toContain("未开启隧道，请及时卸载 FRPS");
    expect(row).toContain("待卸载");
    expect(row).toContain("卸载组件");
  });

  it("活动隧道占用时禁用受管 FRPS 卸载", () => {
    const markup = renderRuntime("active", [], {
      frps: true,
      frpsManagedPresent: true,
      frpsPresent: true,
      frpsInUse: true,
      frpsStatus: "running",
    });
    const row = markup.split("FRPS 穿透服务")[1]?.split('class="ops-service-row"')[0];
    expect(row).toContain("隧道使用中");
    expect(row).toContain("请先停止此服务器上的内网穿透");
    const uninstall = [...(row ?? "").matchAll(/<button([^>]*)>(.*?)<\/button>/g)].find((button) =>
      button[2].includes("卸载组件"),
    );
    expect(uninstall?.[1]).toContain('disabled=""');
  });
});

describe("Jenkins 访问入口与运行环境", () => {
  it("未安装 Jenkins 时展示安装组件且不展示访问入口按钮", () => {
    const markup = renderRuntime("active", [], {
      jenkins: false,
      jenkinsStatus: "not_installed",
    });
    const row = markup.split("Jenkins 运行器")[1]?.split('class="ops-service-row"')[0];
    expect(row).toContain("安装组件");
    expect(row).not.toContain("访问入口");
    expect(row).toContain("未安装");
  });

  it("已就绪 Jenkins 时在安装组件处展示「访问入口」按钮与「卸载组件」按钮", () => {
    const markup = renderRuntime("active", [], {
      jenkins: true,
    });
    const row = markup.split("Jenkins 运行器")[1]?.split('class="ops-service-row"')[0];
    expect(row).toContain("访问入口");
    expect(row).toContain("卸载组件");
    expect(row).not.toContain("安装组件");
    expect(row).toContain(`data-ops-guide-open="server.jenkins.bind/${server.id}"`);
  });
});

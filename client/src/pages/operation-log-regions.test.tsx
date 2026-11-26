import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { ApplicationBuildSection } from "@/features/projects/ApplicationBuildSection";
import { ApplicationRuntimeSection } from "@/features/projects/ApplicationRuntimeSection";
import { ProjectRoutesTab } from "@/features/projects/ProjectRoutesTab";
import { ServerRuntimeTab } from "@/features/servers/tabs/ServerRuntimeTab";
import { BackupMaintenanceSection } from "@/features/settings/BackupMaintenanceSection";
import { DomainCertificatesSection } from "@/features/settings/DomainCertificatesSection";
import { TunnelHistoryTab } from "@/features/tunnels/TunnelHistoryTab";
import {
  OPERATION_ACTIVITY_QUERY_KEY,
  OperationActivityProvider,
  type OperationScope,
} from "@/shared/operations/activity";
import { renderToStaticMarkup } from "@/test/render";

vi.mock("@/shared/operations/use-confirmed-operation", () => ({
  useConfirmedOperation: () => ({ isPending: false }),
}));
vi.mock("@/shared/operations/operation-tracker", () => ({ useOperationTracker: () => vi.fn() }));
vi.mock("@/features/projects/RouteDialog", () => ({ RouteDialog: () => null }));
vi.mock("@/shared/ui/inspector-drawer", () => ({
  InspectorDrawer: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));

function renderRegion(children: ReactNode, prepare?: (client: QueryClient) => void) {
  const client = new QueryClient();
  client.setQueryData(["servers"], { items: [] });
  client.setQueryData(["domains"], { items: [] });
  client.setQueryData(["certificates"], { items: [] });
  client.setQueryData(OPERATION_ACTIVITY_QUERY_KEY, { items: [], activeCount: 0, unreadCount: 0 });
  prepare?.(client);
  const markup = renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <IqujiOverlayProvider>
        <IqujiNotificationProvider closeLabel="关闭通知">
          <OperationActivityProvider>{children}</OperationActivityProvider>
        </IqujiNotificationProvider>
      </IqujiOverlayProvider>
    </QueryClientProvider>,
  );
  const scopes = client
    .getQueryCache()
    .findAll({ queryKey: ["operation-runs", "history"] })
    .map((query) => query.queryKey[2] as OperationScope);
  return { markup, scopes };
}

describe("区域日志入口", () => {
  it.each([
    undefined,
    "/api/logs/builds/build_example/console",
  ])("console地址为%s时圆点继续引导到实际可用日志入口", (consoleUrl) => {
    const timestamp = "2026-01-01T00:00:00Z";
    const { markup } = renderRegion(
      <ApplicationBuildSection
        applicationId="application_example"
        configured
        dirty={false}
        visible
        onChanged={() => {}}
      />,
      (client) => {
        client.setQueryData(OPERATION_ACTIVITY_QUERY_KEY, {
          items: [
            {
              id: "operation_example",
              action: "request_jenkins_release",
              status: "running",
              targetKind: "application",
              targetId: "application_example",
              createdAt: timestamp,
              updatedAt: timestamp,
              readVersion: `running:${timestamp}`,
              active: true,
              unread: false,
              scope: {
                navigation: "projects",
                applicationId: "application_example",
                serverIds: [],
                reachable: true,
              },
            },
          ],
          activeCount: 1,
          unreadCount: 0,
        });
        client.setQueryData(["application-builds", "application_example"], {
          items: [
            {
              id: "build_example",
              applicationId: "application_example",
              operationRunId: "operation_example",
              status: "executing",
              createdAt: timestamp,
              updatedAt: timestamp,
              consoleUrl,
            },
          ],
        });
      },
    );
    const buttons = [...markup.matchAll(/<button\b[^>]*>[\s\S]*?<\/button>/g)].map(
      (match) => match[0],
    );
    const entry = buttons.find((button) =>
      consoleUrl ? button.includes("Jenkins 日志") : button.includes("ops-build-card__log-button"),
    );
    expect(entry).toBeDefined();
    expect(entry).toContain('data-variant="dot"');
    expect(markup).not.toContain("查看日志");
    expect(buttons.some((button) => button.includes("ops-build-card__title"))).toBe(false);
  });

  it("服务器运行操作日志仅显示当前服务器", () => {
    const { markup, scopes } = renderRegion(
      <ServerRuntimeTab
        server={{
          id: "server_example",
          name: "示例服务器",
          publicHost: "192.0.2.10",
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
        }}
        metrics={{ ssh: true, docker: true }}
        onRefresh={() => {}}
      />,
    );
    expect(markup).toContain("日志");
    expect(scopes).toEqual(
      expect.arrayContaining([{ serverId: "server_example", section: "server-runtime" }]),
    );
    expect(scopes).toHaveLength(1);
  });

  it("应用实际运行状态不展示操作日志入口", () => {
    const { markup, scopes } = renderRegion(
      <ApplicationRuntimeSection
        applicationId="application_example"
        dirty={false}
        hasActiveRoutes={false}
        loading={false}
        error={undefined}
        onChanged={() => {}}
      />,
    );
    expect(markup).not.toContain("操作日志");
    expect(scopes).toEqual([]);
  });

  it("备份与数据维护页面不展示操作日志入口", () => {
    const backupMaintenance = renderRegion(<BackupMaintenanceSection />);
    expect(backupMaintenance.markup).not.toContain(">日志<");
    expect(backupMaintenance.scopes).toEqual([]);
  });

  it("隧道运行详情内嵌展示启动日志", () => {
    const timestamp = "2026-01-01T00:00:00Z";
    const run = {
      id: "tunrun_example",
      tunnelId: "tunnel_example",
      status: "running",
      startOperationRunId: "op_start_example",
      startedAt: timestamp,
      createdAt: timestamp,
      updatedAt: timestamp,
    };
    const { markup } = renderRegion(
      <TunnelHistoryTab initialSelected={run} tunnelId="tunnel_example" />,
      (client) => {
        client.setQueryData(["tunnel-runs", "tunnel_example"], { items: [run], page: 1, total: 1 });
        client.setQueryData(["operation-run", "op_start_example"], {
          id: "op_start_example",
          action: "start_frp_tunnel",
          status: "running",
          targetKind: "frp_tunnel_run",
          targetId: "tunrun_example",
          createdAt: timestamp,
          updatedAt: timestamp,
        });
        client.setQueryData(["operation-logs", "op_start_example", timestamp, "running"], {
          pages: [
            {
              items: [
                {
                  id: "log_1",
                  source: "worker",
                  phase: "start",
                  level: "info",
                  message: "tunnel started",
                  timestamp,
                },
              ],
              page: 1,
              pageSize: 100,
              total: 1,
            },
          ],
          pageParams: [1],
        });
      },
    );
    const buttons = [...markup.matchAll(/<button\b[^>]*>[\s\S]*?<\/button>/g)].map(
      (match) => match[0],
    );
    expect(buttons.some((button) => button.includes("启动日志"))).toBe(false);
    expect(markup).toContain("ops-tunnel-run-logs");
    expect(markup).toContain("op_start_example");
    expect(markup).toContain("tunnel started");
  });

  it("应用路由页提供该应用的发布日志入口", () => {
    const { markup, scopes } = renderRegion(
      <ProjectRoutesTab
        gatewayReady={false}
        applicationId="application_example"
        routes={[]}
        domains={[]}
        services={[]}
        onChanged={() => {}}
        dirty={false}
      />,
    );
    expect(scopes).toEqual([
      { applicationId: "application_example", section: "application-routes" },
    ]);
    expect(markup).toContain("发布日志");
  });

  it("域名证书提供一个覆盖所有证书的日志入口", () => {
    const { markup, scopes } = renderRegion(<DomainCertificatesSection />, (client) =>
      client.setQueryData(["certificates"], {
        items: [
          {
            id: "certificate_example",
            name: "示例证书",
            domains: ["example.test"],
            status: "valid",
          },
        ],
      }),
    );
    expect(markup).toContain("日志");
    expect(scopes).toEqual([{ category: "domain-certificates" }]);
  });

  it("通过构建记录的日志按钮查看日志", () => {
    const { markup } = renderRegion(
      <ApplicationBuildSection
        applicationId="application_example"
        configured
        dirty={false}
        visible
        onChanged={() => {}}
      />,
      (client) =>
        client.setQueryData(["application-builds", "application_example"], {
          items: [
            {
              id: "build_example",
              applicationId: "application_example",
              operationRunId: "operation_example",
              status: "failed",
              createdAt: "2026-01-01T00:00:00Z",
              updatedAt: "2026-01-01T00:00:00Z",
            },
          ],
        }),
    );
    expect(markup).not.toContain("查看日志");
    expect(markup).toMatch(/<button[^>]*ops-build-card__log-button[^>]*>/);
    expect(markup).not.toMatch(/<button[^>]*ops-build-card__title[^>]*>/);
  });
});

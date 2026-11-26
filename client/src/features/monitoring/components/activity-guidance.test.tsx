import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import {
  OPERATION_ACTIVITY_QUERY_KEY,
  type OperationActivityItem,
  OperationActivityProvider,
} from "@/shared/operations/activity";
import { renderToStaticMarkup } from "@/test/render";
import { calculateSystemHealth } from "../monitoring-utils";
import { MonitoringProjectsGroupView } from "./MonitoringProjectsGroupView";
import { MonitoringServersTable } from "./MonitoringServersTable";
import { MonitoringStatCards } from "./MonitoringStatCards";

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));

const build: OperationActivityItem = {
  id: "operation_build_example",
  action: "request_jenkins_release",
  status: "running",
  targetKind: "application",
  targetId: "application_example",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  readVersion: "running:2026-01-01T00:00:00Z",
  active: true,
  unread: false,
  scope: {
    navigation: "projects",
    projectId: "project_example",
    applicationId: "application_example",
    targetKind: "application",
    targetId: "application_example",
    serverIds: ["server_example"],
  },
};
const serverOperation: OperationActivityItem = {
  ...build,
  id: "operation_server_example",
  action: "install_jenkins",
  targetKind: "server",
  targetId: "server_example",
  scope: { navigation: "servers", section: "server-runtime", serverIds: ["server_example"] },
};

function renderActivity(children: ReactNode, items = [build]) {
  const client = new QueryClient();
  client.setQueryData(OPERATION_ACTIVITY_QUERY_KEY, {
    items,
    activeCount: items.length,
    unreadCount: 0,
  });
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <IqujiOverlayProvider>
        <IqujiNotificationProvider closeLabel="关闭通知">
          <OperationActivityProvider>{children}</OperationActivityProvider>
        </IqujiNotificationProvider>
      </IqujiOverlayProvider>
    </QueryClientProvider>,
  );
}

describe("首页日志圆点引导", () => {
  it("构建只引导项目卡，不误亮服务器或Swarm卡", () => {
    const markup = renderActivity(
      <MonitoringStatCards
        activeTab="projects"
        onSelectTab={() => {}}
        summary={calculateSystemHealth()}
      />,
    );
    expect(markup.match(/aria-label="1 项正在排队或执行/g)).toHaveLength(1);
    expect(markup).toMatch(
      /data-tone="info"[^>]*data-variant="dot"|data-variant="dot"[^>]*data-tone="info"/,
    );
  });

  it("项目群组及其应用行都保留进入日志的下一层引导", () => {
    const markup = renderActivity(
      <MonitoringProjectsGroupView
        isLoading={false}
        onlyAbnormal={false}
        searchKeyword=""
        items={[
          {
            projectId: "project_example",
            projectName: "示例项目",
            applicationId: "application_example",
            applicationName: "示例应用",
            status: "running",
            runningContainers: 1,
            alerts: [],
          },
        ]}
      />,
    );
    expect(markup.match(/aria-label="1 项正在排队或执行/g)).toHaveLength(2);
  });

  it("服务器监控表仅在自有任务所在行展示活动点", () => {
    const markup = renderActivity(
      <MonitoringServersTable
        isLoading={false}
        onlyAbnormal={false}
        searchKeyword=""
        items={[
          {
            serverId: "server_example",
            serverName: "示例服务器",
            agentStatus: "online",
            alerts: [],
          },
        ]}
      />,
      [serverOperation],
    );
    expect(markup.match(/aria-label="1 项正在排队或执行/g)).toHaveLength(1);
  });
});

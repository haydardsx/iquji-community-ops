import {
  IqujiButton,
  IqujiNotificationProvider,
  IqujiOverlayProvider,
} from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { OperationScope } from "@/shared/operations/log-api";
import { renderToStaticMarkup } from "@/test/render";
import { MonitoringPage } from "./monitoring";
import { ProjectsPage } from "./projects";
import { ServersPage } from "./servers";
import { TunnelsPage } from "./tunnels";

vi.mock("@/app/shell", () => ({ useShell: () => ({ sidebarCollapsed: false }) }));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  useNavigate: () => vi.fn(),
  useSearch: () => ({ search: "", appSearch: "", applicationTab: "source" }),
}));
vi.mock("@/shared/operations/use-confirmed-operation", () => ({
  useConfirmedOperation: () => ({ isPending: false, submit: vi.fn() }),
}));
vi.mock("@/shared/ui/operation-history", () => ({
  OperationHistoryButton: ({ scope }: { scope: OperationScope }) => (
    <IqujiButton data-log-navigation={scope.navigation}>日志</IqujiButton>
  ),
}));

describe("分类侧栏操作", () => {
  it.each([
    ["projects", ProjectsPage],
    ["servers", ServersPage],
    ["tunnels", TunnelsPage],
  ] as const)("%s 侧栏仅保留资源管理入口，不展示范围日志", (navigation, Page) => {
    const client = new QueryClient();
    client.setQueryData(["projects"], { items: [] });
    client.setQueryData(["servers"], { items: [] });
    client.setQueryData(["tunnels"], { items: [] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <IqujiNotificationProvider closeLabel="关闭通知">
            <Page />
          </IqujiNotificationProvider>
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );
    expect(markup).not.toContain(`data-log-navigation="${navigation}"`);
    expect(markup).not.toContain("data-log-navigation=");
  });

  it("首页监控工作台不展示操作日志按钮", () => {
    const client = new QueryClient();
    client.setQueryData(["monitoring-projects"], { items: [] });
    client.setQueryData(["monitoring-servers"], { items: [] });
    client.setQueryData(["monitoring-swarm"], {});
    client.setQueryData(["monitoring-agents"], { items: [] });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <IqujiNotificationProvider closeLabel="关闭通知">
            <MonitoringPage />
          </IqujiNotificationProvider>
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );
    expect(markup).not.toContain("data-log-navigation=");
    expect(markup).not.toContain(">日志<");
  });
});

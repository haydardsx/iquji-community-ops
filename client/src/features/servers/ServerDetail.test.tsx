import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { Server } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { ServerDetail } from "./ServerDetail";

vi.mock("@tanstack/react-router", () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: "/" }),
}));

vi.mock("@/shared/operations/use-confirmed-operation", () => ({
  useConfirmedOperation: () => ({ isPending: false }),
}));

const server: Server = {
  id: "server_test",
  name: "示例服务器",
  publicHost: "192.0.2.10",
  port: 22,
  sshUser: "operator",
  status: "idle",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

describe("ServerDetail 头部操作菜单", () => {
  it("将多个操作收拢至操作下拉菜单中展示", () => {
    const client = new QueryClient();
    client.setQueryData(["server-health", server.id], { serverHealth: { metrics: {} } });
    client.setQueryData(["server-resources", server.id], {});
    client.setQueryData(["secret-assets"], { items: [] });

    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <IqujiNotificationProvider closeLabel="关闭通知">
            <ServerDetail server={server} onEdit={vi.fn()} onDelete={vi.fn()} />
          </IqujiNotificationProvider>
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );

    expect(markup.includes("示例服务器")).toBe(true);
    expect(
      markup.includes("ssh -p &#x27;22&#x27; -l &#x27;operator&#x27; -- &#x27;192.0.2.10&#x27;"),
    ).toBe(true);
    expect(markup.includes("操作")).toBe(true);
    expect(markup.includes("ops-server-header__actions")).toBe(true);
  });

  it("在数据加载中时在头部展示精简的同步中徽标，不展示庞大的独立占位框", () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    // 不填充 query 数据，使 health 和 resources 保持在 loading 状态
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <IqujiNotificationProvider closeLabel="关闭通知">
            <ServerDetail server={server} onEdit={vi.fn()} onDelete={vi.fn()} />
          </IqujiNotificationProvider>
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );

    expect(markup.includes("ops-server-sync-badge")).toBe(true);
    expect(markup.includes("ops-server-sync-spinner")).toBe(true);
    expect(markup.includes("正在同步...")).toBe(true);
    expect(markup.includes("iquji-loading-state")).toBe(false);
  });
});

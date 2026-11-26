import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { Server } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { ServerConnectionDrawer } from "./ServerConnectionDrawer";

vi.mock("@/shared/ui/inspector-drawer", () => ({
  InspectorDrawer: ({
    children,
    title,
    footer,
  }: {
    children: ReactNode;
    title: ReactNode;
    footer?: ReactNode;
  }) => (
    <div data-testid="inspector-drawer">
      <div data-testid="drawer-title">{title}</div>
      <div data-testid="drawer-body">{children}</div>
      {footer ? <div data-testid="drawer-footer">{footer}</div> : null}
    </div>
  ),
}));

const server: Server = {
  id: "server_test",
  name: "示例服务器",
  publicHost: "192.0.2.20",
  port: 22,
  sshUser: "root",
  status: "idle",
  connectionProxy: {
    mode: "manual",
    url: "http://127.0.0.1:7890",
  },
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

describe("ServerConnectionDrawer 连接代理抽屉", () => {
  it("渲染连接代理抽屉标题与配置表单", () => {
    const client = new QueryClient();
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <IqujiNotificationProvider closeLabel="关闭通知">
            <ServerConnectionDrawer server={server} onClose={vi.fn()} onSaved={vi.fn()} />
          </IqujiNotificationProvider>
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );

    expect(markup).toContain("连接代理 · 示例服务器");
    expect(markup).toContain("http://127.0.0.1:7890");
  });
});

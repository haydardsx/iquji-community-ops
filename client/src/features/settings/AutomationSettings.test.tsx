import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import * as externalUrl from "@/shared/desktop/external-url";
import { renderToStaticMarkup } from "@/test/render";
import { commands } from "../../../../server/internal/automation/commands.json";
import { AutomationSettings } from "./AutomationSettings";
import { McpClientDrawer } from "./McpClientDrawer";
import { McpConfigDialog } from "./McpConfigDialog";

vi.mock("@iquji-community/web-component", async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>();
  return {
    ...actual,
    IqujiDrawer: ({
      open,
      title,
      children,
      actions,
      headerMeta,
    }: {
      open: boolean;
      title?: ReactNode;
      children?: ReactNode;
      actions?: ReactNode;
      headerMeta?: ReactNode;
    }) =>
      open ? (
        <div data-testid="iquji-drawer">
          <h2>{title}</h2>
          {headerMeta}
          <div>{children}</div>
          <div>{actions}</div>
        </div>
      ) : null,
    IqujiDialog: ({
      open,
      title,
      description,
      children,
      actions,
    }: {
      open: boolean;
      title?: ReactNode;
      description?: ReactNode;
      children?: ReactNode;
      actions?: ReactNode;
    }) =>
      open ? (
        <div data-testid="iquji-dialog">
          <h2>{title}</h2>
          {description ? <p>{description}</p> : null}
          <div>{children}</div>
          <div>{actions}</div>
        </div>
      ) : null,
  };
});

vi.mock("@/shared/desktop/external-url", () => ({ isTauriDesktop: vi.fn(() => true) }));

function renderWithProviders(children: ReactNode, locale: "zh-CN" | "en-US" = "zh-CN") {
  return renderToStaticMarkup(
    <IqujiOverlayProvider>
      <IqujiNotificationProvider closeLabel="关闭通知">{children}</IqujiNotificationProvider>
    </IqujiOverlayProvider>,
    locale,
  );
}

it.each([
  ["zh-CN", "MCP 设置", "新建客户端"],
  ["en-US", "MCP Settings", "New client"],
] as const)("主视图保留主标题与新建客户端入口，不再内嵌重复表单（%s）", (lang, title, createLabel) => {
  const html = renderWithProviders(<AutomationSettings />, lang);
  expect(html).toContain(`>${title}</h1>`);
  expect(html).toContain(createLabel);
  expect(html).not.toContain("外部软件调用本软件提供的 MCP 服务");
  expect(html).not.toContain("ops-mcp-client-name");
});

it("非桌面环境使用标准空态提示并保留主标题", () => {
  vi.mocked(externalUrl.isTauriDesktop).mockReturnValueOnce(false);
  const html = renderWithProviders(<AutomationSettings />, "zh-CN");
  expect(html).toContain("MCP 设置");
  expect(html).toContain("仅支持桌面窗口");
  expect(html).toContain("仅桌面客户端支持管理本地 MCP 授权。");
  expect(html).not.toContain("新建客户端");
});

it("客户端抽屉与一次性连接配置弹窗可正常渲染", () => {
  const drawerHtml = renderWithProviders(
    <McpClientDrawer
      busy={false}
      client={null}
      commands={commands}
      error=""
      onClose={vi.fn()}
      onSave={vi.fn(async () => {})}
      open
    />,
    "zh-CN",
  );
  expect(drawerHtml).toContain("新建客户端");
  expect(drawerHtml).toContain("客户端名称");
  expect(drawerHtml).toContain("工具权限");
  expect(drawerHtml).toContain("创建客户端");

  const configHtml = renderWithProviders(
    <McpConfigDialog
      clientName="Claude Desktop"
      config={{ mcpServers: { iquops: { command: "iquops-mcp" } } }}
      onClose={vi.fn()}
    />,
    "zh-CN",
  );
  expect(configHtml).toContain("连接配置已生成");
  expect(configHtml).toContain("复制配置");
  expect(configHtml).toContain("连接配置含访问令牌，仅在本次创建时显示");
});

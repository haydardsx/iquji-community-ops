import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { ApplicationTemplateDrawer } from "./ApplicationTemplateDrawer";
import { ApplicationTemplatesSection } from "./ApplicationTemplatesSection";

vi.mock("@iquji-community/web-component", async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>();
  return {
    ...actual,
    IqujiDrawer: ({
      open,
      title,
      description,
      children,
      actions,
    }: {
      open: boolean;
      title: React.ReactNode;
      description?: React.ReactNode;
      children: React.ReactNode;
      actions?: React.ReactNode;
    }) =>
      open ? (
        <div data-testid="iquji-drawer">
          <div data-testid="drawer-title">{title}</div>
          {description ? <div data-testid="drawer-desc">{description}</div> : null}
          <div data-testid="drawer-body">{children}</div>
          {actions ? <div data-testid="drawer-actions">{actions}</div> : null}
        </div>
      ) : null,
  };
});

vi.mock("@/shared/api/client", () => ({
  fetchJson: vi.fn(),
  errorMessage: (err: unknown) => String(err),
}));

const mockTemplates = {
  version: 1 as const,
  items: [
    {
      id: "gitea",
      name: "Gitea",
      description: "提供 Git 仓库、HTTP 与 SSH 访问。",
      config: {
        sourceConfig: { provider: "image", image: "gitea/gitea:1.22" },
        swarmRuntime: {
          ports: [{ name: "http", containerPort: 3000, protocol: "tcp" }],
        },
        environmentVariables: [{ key: "RUN_MODE", plainValue: "prod" }],
      },
    },
    {
      id: "pmail",
      name: "PMail",
      description: "通过 SMTP 和 IMAP 收发邮件。",
      config: {
        sourceConfig: { provider: "image", image: "ghcr.io/jinnrry/pmail:v2.9.9" },
        swarmRuntime: {
          ports: [{ name: "smtp", containerPort: 25, protocol: "tcp" }],
        },
      },
    },
  ],
};

function renderWithProviders(ui: React.ReactNode, templates = mockTemplates) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  queryClient.setQueryData(["application-templates"], templates);

  return renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <IqujiOverlayProvider>
        <IqujiNotificationProvider closeLabel="关闭通知">{ui}</IqujiNotificationProvider>
      </IqujiOverlayProvider>
    </QueryClientProvider>,
  );
}

describe("ApplicationTemplatesSection", () => {
  it("renders template list with cards, badges, and metadata tags", () => {
    const html = renderWithProviders(<ApplicationTemplatesSection />);

    // 标题与操作栏
    expect(html).toContain("应用模板");
    expect(html).toContain("导入模板");
    expect(html).toContain("导出模板");
    expect(html).toContain("添加");

    // 搜索工具栏与统计
    expect(html).toContain("搜索模板名称、标识或描述...");
    expect(html).toContain("共 2 个模板");

    // 模板卡片内容
    expect(html).toContain("Gitea");
    expect(html).toContain("gitea");
    expect(html).toContain("gitea/gitea:1.22");
    expect(html).toContain("1 个端口");
    expect(html).toContain("1 个变量");

    expect(html).toContain("PMail");
    expect(html).toContain("pmail");
    expect(html).toContain("ghcr.io/jinnrry/pmail:v2.9.9");
  });

  it("renders empty state when there are no templates", () => {
    const html = renderWithProviders(<ApplicationTemplatesSection />, { version: 1, items: [] });

    expect(html).toContain("暂无应用模板");
    expect(html).toContain("创建或导入应用模板");
  });
});

describe("ApplicationTemplateDrawer", () => {
  it("renders drawer in edit mode with locked ID badge", () => {
    const html = renderWithProviders(
      <ApplicationTemplateDrawer
        draft={{
          original: "gitea",
          id: "gitea",
          name: "Gitea",
          description: "提供 Git 仓库",
          config: JSON.stringify({ sourceConfig: { image: "gitea:latest" } }, null, 2),
        }}
        onClose={vi.fn()}
        onSave={vi.fn()}
        open={true}
      />,
    );

    expect(html).toContain("编辑应用模板 · Gitea");
    expect(html).toContain("已锁定");
    expect(html).toContain("部署配置 (JSON)");
    expect(html).toContain("格式正确");
    expect(html).toContain("保存");
    expect(html).toContain("取消");
  });

  it("renders drawer in create mode allowing ID editing", () => {
    const html = renderWithProviders(
      <ApplicationTemplateDrawer
        draft={{
          original: null,
          id: "my-app",
          name: "My App",
          description: "New template description",
          config: "{}",
        }}
        onClose={vi.fn()}
        onSave={vi.fn()}
        open={true}
      />,
    );

    expect(html).toContain("新建应用模板");
    expect(html).not.toContain("已锁定");
    expect(html).toContain("模板的唯一英文标识，创建后不可修改。");
    expect(html).toContain("保存");
  });
});

import { IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { OperationScope } from "@/shared/operations/log-api";
import { renderToStaticMarkup } from "@/test/render";
import { OperationHistoryButton } from "./operation-history";

vi.mock("@/shared/ui/inspector-drawer", () => ({
  InspectorDrawer: ({
    children,
    footer,
    headerToolbar,
  }: {
    children: ReactNode;
    footer?: ReactNode;
    headerToolbar?: ReactNode;
  }) => (
    <div>
      {headerToolbar}
      {children}
      {footer}
    </div>
  ),
}));

describe("操作历史展示", () => {
  it.each<OperationScope>([
    { projectId: "project_example" },
    { applicationId: "application_example" },
    { serverId: "server_example" },
    { targetKind: "certificate", targetId: "certificate_example" },
    { tunnelId: "tunnel_example" },
    { category: "domain-certificates" },
  ])("日志入口 %# 显示当前作用域的记录", (scope) => {
    const client = new QueryClient();
    client.setQueryData(["operation-runs", "history", scope, 1, ""], {
      items: [
        {
          id: "operation_example",
          action: "start_application",
          status: "running",
          targetKind: "application",
          targetId: "application_example",
          targetName: "示例运行目标",
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
        },
      ],
      page: 1,
      pageSize: 20,
      total: 1,
    });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <OperationHistoryButton scope={scope} />
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );
    expect(markup).toContain("示例运行目标");
    expect(markup).toContain("查看日志");
    expect(markup).not.toContain("暂无操作记录");
  });

  it("同一入口展示排队、进行中、成功、失败和取消记录", () => {
    const statuses = ["queued", "running", "succeeded", "failed", "cancelled"];
    const items = statuses.map((status) => ({
      id: `operation_${status}`,
      action: "create_secret_asset",
      status,
      targetKind: "secret_asset",
      targetId: "secret_example",
      targetName: `示例凭据 ${status}`,
      createdAt: "2026-01-01T00:00:00Z",
      updatedAt: "2026-01-01T00:00:00Z",
    }));
    const client = new QueryClient();
    client.setQueryData(["operation-runs", "history", { category: "secrets" }, 1, ""], {
      items,
      page: 1,
      pageSize: 20,
      total: 5,
    });
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <OperationHistoryButton scope={{ category: "secrets" }} />
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );
    for (const status of statuses) expect(markup).toContain(`示例凭据 ${status}`);
    expect(markup).toContain("排队中");
    expect(markup).toContain("进行中");
    expect(markup).toContain("成功");
    expect(markup).toContain("已取消");
    expect(markup).toContain("iquji-pagination");
    expect(markup).toContain("显示 1–5 条，共 5 条");
    expect(markup.match(/>查看日志/g)).toHaveLength(5);
  });

  it("单条记录显示分页摘要、日志入口和可用操作", () => {
    const client = new QueryClient();
    const run = {
      id: "operation_example",
      action: "rotate_secret_asset",
      status: "succeeded",
      targetKind: "secret_asset",
      targetId: "secret_example",
      targetName: "示例凭据",
      createdAt: "2026-01-01T00:00:00Z",
      updatedAt: "2026-01-01T00:01:00Z",
    };
    client.setQueryData(["operation-runs", "history", { category: "secrets" }, 1, ""], {
      items: [run],
      page: 1,
      pageSize: 20,
      total: 1,
    });
    const actions = vi.fn(() => null);
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <OperationHistoryButton scope={{ category: "secrets" }} renderActions={actions} />
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );
    expect(markup).toContain("示例凭据");
    expect(markup).toContain("显示 1–1 条，共 1 条");
    expect(markup.match(/class="iquji-pagination(?: |")/g)).toHaveLength(1);
    expect(markup.match(/>查看日志/g)).toHaveLength(1);
    expect(actions).toHaveBeenCalledTimes(1);
    expect(actions).toHaveBeenCalledWith(run, [run], 0);
  });
});

import { IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { OperationRun } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { ServerOperationHistory } from "./ServerOperationHistory";

function renderHistory(items: OperationRun[]) {
  const client = new QueryClient();
  client.setQueryData(
    ["operation-runs", "history", { serverId: "server_worker", section: "server-runtime" }, 1, ""],
    {
      items,
      page: 1,
      pageSize: 20,
      total: items.length,
    },
  );
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <IqujiOverlayProvider>
        <ServerOperationHistory serverId="server_worker" />
      </IqujiOverlayProvider>
    </QueryClientProvider>,
  );
}

describe("服务器日志入口", () => {
  it("已有操作时仅显示日志入口，诊断内容收进抽屉", () => {
    const markup = renderHistory([
      {
        id: "operation_join",
        targetKind: "server",
        targetId: "server_worker",
        action: "install_docker_swarm",
        status: "failed",
        errorCode: "SWARM_MANAGER_ENDPOINT_UNAVAILABLE",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:01:00Z",
      },
    ]);
    expect(markup).toContain("日志");
    expect(markup).not.toContain("节点间路由");
    expect(markup).not.toContain("查看诊断日志");
    expect(markup).not.toContain("SWARM_MANAGER_ENDPOINT_UNAVAILABLE");
  });

  it("没有操作时保留日志入口，页面不展示空状态面板", () => {
    const markup = renderHistory([]);
    expect(markup).toContain("日志");
    expect(markup).not.toContain("暂无失败操作");
  });
});

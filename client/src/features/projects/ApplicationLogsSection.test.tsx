import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { ApplicationLogsSection } from "./ApplicationLogsSection";

describe("ApplicationLogsSection 日志组件分页与固定布局", () => {
  it("日志加载可用时渲染固定包装器、单页数量选择与分页控制条", () => {
    const client = new QueryClient();
    const mockItems = Array.from({ length: 65 }, (_, i) => ({
      id: `log-${i}`,
      timestamp: "2026-01-01T12:00:00Z",
      source: "main",
      serviceSlotKey: "main",
      message: `log line content ${i}`,
    }));

    client.setQueryData(["application-service-logs", "app-1", "main"], {
      availability: "available",
      items: mockItems,
    });

    const html = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <ApplicationLogsSection
          applicationId="app-1"
          services={[{ serviceName: "主服务", slotKey: "main" }]}
        />
      </QueryClientProvider>,
    );

    expect(html).toContain("容器运行日志");
    expect(html).toContain("应用运行日志");
    // 检查固定包装器
    expect(html).toContain("ops-application-logs__viewer-wrapper");
    // 检查单页数量选择
    expect(html).toContain("ops-application-logs__page-size");
    // 检查分页条和汇总行数
    expect(html).toContain("ops-application-logs__pagination-bar");
    expect(html).toContain("第 1 - 50 行，共 65 行");
    expect(html).toContain("上一页");
    expect(html).toContain("下一页");
    expect(html).toContain("1");
    expect(html).toContain("2");
  });

  it("日志首次加载尚未返回数据时渲染加载状态容器与提示文案", () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });

    const html = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <ApplicationLogsSection
          applicationId="app-1"
          services={[{ serviceName: "主服务", slotKey: "main" }]}
        />
      </QueryClientProvider>,
    );

    expect(html).toContain("ops-application-logs__loading");
    expect(html).toContain("正在读取运行日志…");
    expect(html).not.toContain("ops-application-logs__viewer-wrapper");
  });
});

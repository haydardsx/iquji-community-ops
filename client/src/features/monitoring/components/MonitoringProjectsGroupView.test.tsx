import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { MonitoringProjectsGroupView } from "./MonitoringProjectsGroupView";

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));

describe("MonitoringProjectsGroupView 应用运行状态展示", () => {
  it("当应用状态为 success 时展示绿色 success 状态及呼吸灯", () => {
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={new QueryClient()}>
        <IqujiOverlayProvider>
          <IqujiNotificationProvider closeLabel="关闭通知">
            <MonitoringProjectsGroupView
              isLoading={false}
              onlyAbnormal={false}
              searchKeyword=""
              items={[
                {
                  projectId: "proj_1",
                  projectName: "测试项目",
                  applicationId: "app_1",
                  applicationName: "测试应用",
                  status: "success",
                  runningContainers: 1,
                  alerts: [],
                },
              ]}
            />
          </IqujiNotificationProvider>
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );

    expect(markup).toContain('class="ops-status-dot ops-status-dot--pulse" data-tone="success"');
    expect(markup).toContain('data-tone="success"');
    expect(markup).toContain("运行中");
    expect(markup).not.toContain('data-tone="neutral"');
  });
});

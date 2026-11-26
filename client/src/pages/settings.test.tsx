import { IqujiNotificationProvider } from "@iquji-community/web-component";
import { IqujiThemeProvider } from "@iquji-community/web-theme";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it } from "vitest";
import { OperationActivityProvider } from "@/shared/operations/activity";
import { OperationTrackerProvider } from "@/shared/operations/operation-tracker";
import { renderToStaticMarkup } from "@/test/render";
import { SettingsPage } from "./settings";

function renderSettings(locale: "zh-CN" | "en-US" = "zh-CN") {
  const queryClient = new QueryClient();
  return renderToStaticMarkup(
    <IqujiThemeProvider>
      <QueryClientProvider client={queryClient}>
        <IqujiNotificationProvider closeLabel="关闭通知">
          <OperationActivityProvider>
            <OperationTrackerProvider>
              <SettingsPage />
            </OperationTrackerProvider>
          </OperationActivityProvider>
        </IqujiNotificationProvider>
      </QueryClientProvider>
    </IqujiThemeProvider>,
    locale,
  );
}

it("设置导航包含运行与集成和合并后的备份与数据维护，并移除服务守护与独立数据维护项", () => {
  const html = renderSettings("zh-CN");
  expect(html).toContain("偏好设置");
  expect(html).toContain("资产");
  expect(html).toContain("网络与连接");
  expect(html).toContain("系统与维护");
  expect(html).toContain("运行与集成");
  expect(html).toContain("备份与数据维护");
  expect(html).toContain("MCP 设置");
  expect(html).not.toContain(">服务守护<");
  expect(html).not.toContain(">数据维护<");
  expect(html).not.toContain(">MCP 服务器<");
});

it("设置导航支持英文分组与 MCP 设置标题", () => {
  const html = renderSettings("en-US");
  expect(html).toContain("System &amp; Maintenance");
  expect(html).toContain("Runtime &amp; Integrations");
  expect(html).toContain("Backup &amp; Maintenance");
  expect(html).toContain("MCP Settings");
  expect(html).not.toContain(">MCP Server<");
});

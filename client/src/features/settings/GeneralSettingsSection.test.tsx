import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { GeneralSettingsSection } from "./GeneralSettingsSection";

function renderWithQuery(
  locale: "zh-CN" | "en-US" = "zh-CN",
  publicBaseUrl = "http://127.0.0.1:18080",
) {
  const queryClient = new QueryClient();
  queryClient.setQueryData(["local-config"], {
    controlPlane: {
      hostname: "ops.local",
      apiHost: "127.0.0.1",
      apiPort: 18080,
      publicBaseUrl,
      dataDirectory: "./state/ops-data",
      version: "0.2.1",
    },
  });
  queryClient.setQueryData(["control-plane-health"], { ok: true });

  return renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <GeneralSettingsSection />
    </QueryClientProvider>,
    locale,
  );
}

it("通用设置桌面回环地址不去重显示外部地址并移除冗余托管行", () => {
  const html = renderWithQuery("zh-CN", "http://127.0.0.1:18080");
  expect(html).toContain(">通用设置</h1>");
  expect(html).toContain("控制面");
  expect(html).toContain("0.2.1");
  expect(html).toContain("127.0.0.1:18080");
  expect(html).not.toContain("外部访问地址");
  expect(html).not.toContain("公网访问基地址");
  expect(html).toContain("./state/ops-data");
  expect(html).toContain("本地服务");
  expect(html).toContain("iquops-server");
  expect(html).toContain("iquops-worker");
  expect(html).toContain("已连接");
  expect(html).toContain("桌面托管");
  expect(html).not.toContain("重新启动服务");
});

it("存在独立外部访问地址时正常展示", () => {
  const html = renderWithQuery("zh-CN", "https://ops.example.test");
  expect(html).toContain("外部访问地址");
  expect(html).toContain("https://ops.example.test");
});

it("通用设置支持英文显示", () => {
  const html = renderWithQuery("en-US", "https://ops.example.test");
  expect(html).toContain(">General</h1>");
  expect(html).toContain("Control Plane");
  expect(html).toContain("External URL");
  expect(html).toContain("Local Services");
  expect(html).toContain("Connected");
  expect(html).toContain("Managed by desktop");
});

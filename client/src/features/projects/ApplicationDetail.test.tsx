import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { Application, ProjectWorkspace } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { ApplicationDetail } from "./ApplicationDetail";

vi.mock("@tanstack/react-router", () => ({
  useBlocker: () => ({ status: "idle", proceed: vi.fn(), reset: vi.fn() }),
  useNavigate: () => vi.fn(),
  useRouter: () => ({ navigate: vi.fn() }),
}));

vi.mock("@/shared/operations/use-confirmed-operation", () => ({
  useConfirmedOperation: () => ({ isPending: false, submit: vi.fn(), reset: vi.fn() }),
}));

vi.mock("@/shared/operations/operation-tracker", () => ({
  useOperationTracker: () => vi.fn(),
}));

const timestamp = "2026-01-01T00:00:00Z";
const application: Application = {
  id: "application_example",
  projectId: "project_example",
  name: "example-app",
  createdAt: timestamp,
  updatedAt: timestamp,
};

const workspace: ProjectWorkspace = {
  project: {
    id: "project_example",
    name: "示例项目",
    createdAt: timestamp,
    updatedAt: timestamp,
  },
  applications: [application],
  routes: [],

  servers: [],
  applicationDeploymentConfigs: [],
};

describe("ApplicationDetail 底部保存栏", () => {
  it("没有未保存修改时不渲染底部保存横条", () => {
    const client = new QueryClient();
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <IqujiNotificationProvider closeLabel="关闭通知">
            <ApplicationDetail
              application={application}
              onChanged={() => {}}
              onClose={() => {}}
              onDeleted={() => {}}
              onTabChange={() => {}}
              tab="source"
              workspace={workspace}
            />
          </IqujiNotificationProvider>
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );
    expect(markup).not.toContain("ops-application-page__footer");
    expect(markup).not.toContain("配置已保存");
    expect(markup).not.toContain("编辑应用来源");
    expect(markup).toMatch(
      /<button\b(?=[^>]*data-ops-guide-field="source\.provider")(?=[^>]*data-ops-guide-open="application\.source")[^>]*>/,
    );
  });

  it("顶部工具栏将容器终端收拢到操作菜单内", () => {
    const client = new QueryClient();
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <IqujiNotificationProvider closeLabel="关闭通知">
            <ApplicationDetail
              application={application}
              onChanged={() => {}}
              onClose={() => {}}
              onDeleted={() => {}}
              onTabChange={() => {}}
              tab="source"
              workspace={workspace}
            />
          </IqujiNotificationProvider>
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );
    expect(markup).toContain("ops-application-page__toolbar");
    expect(markup).not.toContain("<button>容器终端</button>");
  });
});

describe("应用变量设置", () => {
  it.each([
    ["zh-CN", "变量设置", "输入变量", "输出变量"],
    ["en-US", "Variable settings", "Input variables", "Output variables"],
  ] as const)("在 %s 下用一个标签显示输入和输出表格", (locale, label, inputs, outputs) => {
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={new QueryClient()}>
        <IqujiOverlayProvider>
          <IqujiNotificationProvider closeLabel="关闭通知">
            <ApplicationDetail
              application={application}
              onChanged={() => {}}
              onClose={() => {}}
              onDeleted={() => {}}
              onTabChange={() => {}}
              tab="variables"
              workspace={{
                ...workspace,
                applicationDeploymentConfigs: [
                  {
                    applicationId: application.id,
                    environmentVariables: [
                      {
                        key: "DEMO_FLAG",
                        scope: "global",
                        valueSource: "plain_text",
                        plainValue: "sample",
                      },
                    ],
                    outputVariables: [
                      {
                        id: "out_example",
                        key: "PUBLIC_URL",
                        visibility: "project",
                        valueSource: "plain_text",
                        plainValue: "https://example.test",
                      },
                    ],
                  },
                ],
              }}
            />
          </IqujiNotificationProvider>
        </IqujiOverlayProvider>
      </QueryClientProvider>,
      locale,
    );
    expect(markup).toContain(`>${label}</`);
    expect(markup).toContain(`aria-label="${inputs}"`);
    expect(markup).toContain(`aria-label="${outputs}"`);
    expect(markup).toContain('data-tab="variables"');
    expect(markup).toContain('name="variables.0.key"');
    expect(markup).toContain('name="outputs.0.key"');
    expect(markup).not.toContain('class="ops-variable-guide"');
  });
});

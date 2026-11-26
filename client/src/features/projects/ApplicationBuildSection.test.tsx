import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { ApplicationBuildArtifacts } from "./ApplicationBuildArtifacts";
import { ApplicationBuildSection } from "./ApplicationBuildSection";
import type { ApplicationBuild } from "./build-api";

vi.mock("@/shared/operations/use-confirmed-operation", () => ({
  useConfirmedOperation: () => ({ isPending: false }),
}));
vi.mock("@/shared/operations/operation-tracker", () => ({
  useOperationTracker: () => vi.fn(),
}));

function renderBuildSection(
  status: string,
  options?: { configured?: boolean; dirty?: boolean; provider?: string },
) {
  const client = new QueryClient();
  const run: ApplicationBuild = {
    id: "build_example",
    applicationId: "app_example",
    operationRunId: "operation_example",
    status,
    provider: options?.provider,
    commitSha: "example_fixed_revision",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:01:00Z",
  };
  client.setQueryData(["application-builds", "app_example"], { items: [run] });
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <IqujiOverlayProvider>
        <IqujiNotificationProvider closeLabel="关闭通知">
          <ApplicationBuildSection
            applicationId="app_example"
            configured={options?.configured ?? true}
            dirty={options?.dirty ?? false}
            visible
            onChanged={() => {}}
          />
        </IqujiNotificationProvider>
      </IqujiOverlayProvider>
    </QueryClientProvider>,
  );
}

describe("应用构建记录入口", () => {
  it("逐槽展示实际镜像且不为 Git 产物添加虚构的基础镜像摘要", () => {
    const markup = renderToStaticMarkup(
      <ApplicationBuildArtifacts
        artifacts={[
          {
            serviceSlotKey: "web",
            image: "example/web:release",
            imageDigest: "digest_web",
            platform: "linux/arm64",
          },
          {
            serviceSlotKey: "jobs",
            image: "example/jobs:release",
            imageDigest: "digest_jobs",
            serverId: "node_example",
          },
        ]}
      />,
    );
    for (const fact of [
      "example/web:release",
      "example/jobs:release",
      "digest_web",
      "digest_jobs",
      "linux/arm64",
      "node_example",
    ]) {
      expect(markup).toContain(fact);
    }
    expect(markup).not.toContain("基础镜像");
  });
  it("记录收进抽屉，构建发布入口继续显示", () => {
    const markup = renderBuildSection("failed");
    expect(markup).toContain("构建记录");
    expect(markup).toContain("构建并发布");
    expect(markup).toContain("配置已就绪");
    expect(markup).not.toContain("example_fixed_revision");
    expect(markup).not.toContain("发布编号");
  });

  it("记录收起时仍识别活动构建，保留排队状态提示与构建记录入口", () => {
    const markup = renderBuildSection("executing");
    expect(markup).toContain("当前已有发布任务，新提交的任务将排队执行。");
    expect(markup).toContain("构建记录");
    expect(markup).not.toContain("查看构建记录");
    expect(markup).not.toMatch(/disabled=""[^>]*><span[^>]*>构建并发布/);
  });

  it("按本次快照标记容器发布的运行阶段", () => {
    const markup = renderBuildSection("running", { provider: "image" });
    expect(markup).toContain("部署容器服务");
    expect(markup).not.toContain("Jenkins 队列");
  });

  it("存在未保存修改时展示保存提醒并禁用构建发布", () => {
    const markup = renderBuildSection("succeeded", { dirty: true });
    expect(markup).toContain("请先保存配置，再构建发布。");
    expect(markup).toMatch(/disabled=""[^>]*><span[^>]*>构建并发布/);
  });

  it("配置未完善时展示补全指引并禁用构建发布", () => {
    const markup = renderBuildSection("succeeded", { configured: false });
    expect(markup).toContain("保存应用来源、部署集群和运行服务后，即可发布。");
    expect(markup).toMatch(/disabled=""[^>]*><span[^>]*>构建并发布/);
  });
});

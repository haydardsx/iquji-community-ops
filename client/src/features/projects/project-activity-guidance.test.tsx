import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { Application, Project } from "@/contracts/types";
import {
  OPERATION_ACTIVITY_QUERY_KEY,
  OperationActivityDot,
  type OperationActivityItem,
  OperationActivityProvider,
} from "@/shared/operations/activity";
import { renderToStaticMarkup } from "@/test/render";
import { ApplicationBuildSection } from "./ApplicationBuildSection";
import { ProjectGeneralTab } from "./ProjectGeneralTab";
import { ProjectTree } from "./ProjectTree";

vi.mock("@/shared/operations/use-confirmed-operation", () => ({
  useConfirmedOperation: () => ({ isPending: false }),
}));
vi.mock("@/shared/operations/operation-tracker", () => ({ useOperationTracker: () => vi.fn() }));

const timestamp = "2026-01-01T00:00:00Z";
const project: Project = {
  id: "project_example",
  name: "示例项目",
  createdAt: timestamp,
  updatedAt: timestamp,
};
const application: Application = {
  id: "application_example",
  projectId: project.id,
  name: "示例应用",
  createdAt: timestamp,
  updatedAt: timestamp,
};
const build: OperationActivityItem = {
  id: "operation_example",
  action: "request_jenkins_release",
  status: "running",
  targetKind: "application",
  targetId: application.id,
  createdAt: timestamp,
  updatedAt: timestamp,
  readVersion: `running:${timestamp}`,
  active: true,
  unread: false,
  scope: {
    navigation: "projects",
    projectId: project.id,
    applicationId: application.id,
    serverIds: [],
  },
};

function renderActivity(children: ReactNode, items = [build]) {
  const client = new QueryClient();
  client.setQueryData(OPERATION_ACTIVITY_QUERY_KEY, {
    items,
    activeCount: items.filter((item) => item.active).length,
    unreadCount: items.filter((item) => item.unread).length,
  });
  client.setQueryData(["project-applications", project.id], { items: [application] });
  client.setQueryData(["application-builds", application.id], { items: [] });
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <IqujiOverlayProvider>
        <IqujiNotificationProvider closeLabel="关闭通知">
          <OperationActivityProvider>{children}</OperationActivityProvider>
        </IqujiNotificationProvider>
      </IqujiOverlayProvider>
    </QueryClientProvider>,
  );
}

function ProjectGuidance() {
  return (
    <>
      <ProjectTree
        projects={[project]}
        onSelectProject={() => {}}
        onSelectApplication={() => {}}
        onCreateApplication={() => {}}
        onEditProject={() => {}}
        onDeleteProject={() => {}}
      />
      <ProjectGeneralTab
        workspace={{ project, applications: [application], routes: [] }}
        onSelectApplication={() => {}}
        search=""
        onSearchChange={() => {}}
      />
      <ApplicationBuildSection
        applicationId={application.id}
        dirty={false}
        configured
        visible
        onChanged={() => {}}
      />
    </>
  );
}

describe("项目逐层活动引导", () => {
  it("构建活动可从项目、应用及其构建记录中看到", () => {
    const markup = renderActivity(<ProjectGuidance />);
    expect(markup.match(/data-variant="dot"/g)).toHaveLength(4);
    expect(markup.match(/aria-label="1 项正在排队或执行/g)).toHaveLength(4);
    expect(markup).toContain("构建记录");
  });

  it("访问入口动作保留上层应用提示，构建记录按钮不会误亮", () => {
    const route = {
      ...build,
      action: "apply_route",
      targetKind: "route",
      targetId: "route_example",
      scope: { ...build.scope, section: "application-routes" as const },
    };
    const markup = renderActivity(<ProjectGuidance />, [route]);
    expect(markup.match(/data-variant="dot"/g)).toHaveLength(3);
  });

  it.each([
    ["succeeded", "success"],
    ["failed", "danger"],
  ] as const)("未读结果 %s 显示对应状态 %s", (status, tone) => {
    const markup = renderActivity(<OperationActivityDot scope={{ projectId: project.id }} />, [
      { ...build, status, active: false, unread: true },
    ]);
    expect(markup).toContain(`data-tone="${tone}"`);
    expect(markup).toContain('data-variant="dot"');
  });

  it("取消未读和已读成功不留下父层圆点", () => {
    const markup = renderActivity(<ProjectGuidance />, [
      { ...build, id: "cancelled", status: "cancelled", active: false, unread: true },
      { ...build, id: "read_success", status: "succeeded", active: false, unread: false },
    ]);
    expect(markup).not.toContain('data-variant="dot"');
  });

  it("目标删除且没有分类入口时不在项目祖先留下孤立提示", () => {
    const markup = renderActivity(<ProjectGuidance />, [
      {
        ...build,
        status: "failed",
        active: false,
        unread: true,
        scope: { ...build.scope, reachable: false },
      },
    ]);
    expect(markup).not.toContain('data-variant="dot"');
  });
});

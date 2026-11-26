import { describe, expect, it, vi } from "vitest";
import type { OperationRun } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { ServerActiveRunsBanner } from "./ServerActiveRunsBanner";

function mockRun(overrides?: Partial<OperationRun>): OperationRun {
  return {
    action: "install_docker_engine",
    createdAt: new Date().toISOString(),
    id: "run-docker-1",
    status: "running",
    summary: "操作已创建",
    targetId: "server-1",
    targetKind: "server",
    updatedAt: new Date().toISOString(),
    ...overrides,
  };
}

describe("ServerActiveRunsBanner", () => {
  it("无活动操作时不渲染任何内容", () => {
    const onCancel = vi.fn();
    const markup = renderToStaticMarkup(<ServerActiveRunsBanner runs={[]} onCancel={onCancel} />);
    expect(markup).toBe("");
  });

  it("渲染任务标题、运行状态 Badge、摘要描述与取消按钮", () => {
    const onCancel = vi.fn();
    const run = mockRun();
    const markup = renderToStaticMarkup(
      <ServerActiveRunsBanner runs={[run]} onCancel={onCancel} />,
    );

    expect(markup).toContain("ops-server-active-banner");
    expect(markup).toContain("操作已创建");
    expect(markup).toContain("取消旧操作");
    expect(markup).toContain("运行中");
  });

  it("排队状态下渲染等待排队 Badge", () => {
    const onCancel = vi.fn();
    const run = mockRun({ status: "queued" });
    const markup = renderToStaticMarkup(
      <ServerActiveRunsBanner runs={[run]} onCancel={onCancel} />,
    );

    expect(markup).toContain("等待中");
  });

  it("禁用状态下取消按钮具有 disabled 属性", () => {
    const onCancel = vi.fn();
    const run = mockRun();
    const markup = renderToStaticMarkup(
      <ServerActiveRunsBanner runs={[run]} disabled onCancel={onCancel} />,
    );

    expect(markup).toMatch(/disabled=""[^>]*><span[^>]*>取消旧操作/);
  });
});

import { describe, expect, it } from "vitest";
import { router } from "@/app/router";
import { tutorials } from "@/shared/tutorial/controller";
import { matchingPageCatalog, workspacePageOrder, workspacePages } from "./pages";

describe("工作区页面合同", () => {
  it("导航及总览教程覆盖正式目录，每个路径都注册为真实路由", () => {
    expect([...workspacePageOrder].sort()).toEqual(Object.keys(workspacePages).sort());
    expect(tutorials.workspace).toEqual(workspacePageOrder);
    for (const id of workspacePageOrder) {
      const { path, tabs } = workspacePages[id];
      expect(router.routesByPath[path]?.fullPath).toBe(path);
      expect(new Set(tabs).size).toBe(tabs.length);
    }
  });
  it("桌面窗口拒绝路径或标签不一致的服务端目录", () => {
    expect(matchingPageCatalog(workspacePages)).toBe(true);
    expect(
      matchingPageCatalog({ ...workspacePages, home: { ...workspacePages.home, tabs: [] } }),
    ).toBe(false);
  });
});

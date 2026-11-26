import contract from "../../../server/internal/automation/commands.json";

export type PageID = keyof typeof contract.pages;
type PagePath<Id extends PageID> = Id extends "home" ? "/" : `/${Id}`;
export type PageCatalog = {
  [Id in PageID]: { path: PagePath<Id>; tabs: readonly string[] };
};
export const workspacePages = contract.pages as PageCatalog;
export const automationLimits = contract.limits;
// 顺序是工作区展示偏好，路径和可操作页签直接来自与服务端共用的正式合同。
export const workspacePageOrder = [
  "home",
  "projects",
  "servers",
  "tunnels",
  "settings",
] as const satisfies readonly PageID[];
export function matchingPageCatalog(input: PageCatalog) {
  return (
    Object.keys(input).length === workspacePageOrder.length &&
    workspacePageOrder.every((id) => {
      const received = input[id],
        expected = workspacePages[id];
      return (
        received?.path === expected.path &&
        Array.isArray(received.tabs) &&
        received.tabs.length === expected.tabs.length &&
        expected.tabs.every((tab, i) => received.tabs[i] === tab)
      );
    })
  );
}

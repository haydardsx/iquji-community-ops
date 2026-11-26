import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  bindTutorialHost,
  closeTutorial,
  manualTutorial,
  operateTutorial,
  tutorials,
} from "./controller";

const invoke = (name: string, args: Record<string, string>, client = "reader") =>
  operateTutorial(`tutorial_${name}`, args, client, "Example assistant");
const workspace = { target: "workspace" };
let path = "/";
let removeHost = () => {};
let foreignDialog: false | "visible" | "hidden" = false;
beforeEach(() => {
  path = "/";
  foreignDialog = false;
  vi.stubGlobal("getComputedStyle", (element: { dialog?: boolean }) => ({
    visibility: element.dialog ? foreignDialog : "visible",
  }));
  const visible = {
    querySelector: () => null,
    isConnected: true,
    closest: () => null,
    getClientRects: () => [{}],
  };
  vi.stubGlobal("document", {
    querySelector: () => visible,
    querySelectorAll: () => (foreignDialog ? [{ ...visible, dialog: true }] : []),
  });
  removeHost = bindTutorialHost({
    t: (key: string) => key,
    page: () => path,
    expand: () => {},
    go: async (page) => {
      path = page === "home" ? "/" : `/${page}`;
    },
  });
});
afterEach(() => {
  closeTutorial();
  removeHost();
  vi.unstubAllGlobals();
});
describe("教程控制", () => {
  it("从登记的步骤定位实际页面并核验调用者所有权", async () => {
    const list = (await invoke("list", {})) as { items: { id: string; steps: unknown[] }[] };
    expect(list.items.map((item) => item.steps.length)).toEqual([tutorials.workspace.length, 3]);
    expect(await invoke("start", { ...workspace, step: "servers" })).toMatchObject({
      active: true,
      page: "servers",
      index: 3,
      available: true,
    });
    expect(path).toBe("/servers");
    await expect(invoke("close", workspace, "other")).rejects.toThrow("AUTOMATION_TUTORIAL_OWNED");
    expect(await invoke("state", {}, "other")).toEqual({ active: false });
    expect(await invoke("next", workspace)).toMatchObject({ step: "tunnels" });
    const hint = await invoke("hint", { ...workspace, step: "settings", message: "Open General" });
    expect(hint).toMatchObject({ hint: { author: "Example assistant", message: "Open General" } });
    expect(await manualTutorial("tutorial_close", workspace)).toEqual({ active: false });
  });
  it("拒绝未登记定位、跨客户端写入、外部链接与超长提示", async () => {
    await expect(invoke("goto", { ...workspace, step: "[role=dialog]" })).rejects.toThrow(
      "AUTOMATION_TUTORIAL_STEP_INVALID",
    );
    for (const message of ["a".repeat(501), "<a>demo</a>", "https://example.test/"]) {
      await expect(invoke("hint", { ...workspace, step: "home", message })).rejects.toThrow(
        "AUTOMATION_TUTORIAL_HINT_INVALID",
      );
    }
    expect(await invoke("state", {})).toEqual({ active: false });
    await manualTutorial("tutorial_start", workspace);
    await expect(invoke("close", workspace)).rejects.toThrow("AUTOMATION_TUTORIAL_OWNED");
  });
  it("仅允许用户真实打开的创建表单进入编辑步骤，并保护既有对话框", async () => {
    await expect(invoke("goto", { target: "project-create", step: "name" })).rejects.toThrow(
      "AUTOMATION_TUTORIAL_FORM_REQUIRED",
    );
    foreignDialog = "visible";
    await expect(invoke("start", { target: "project-create" })).rejects.toThrow(
      "AUTOMATION_DRAFT_ACTIVE",
    );
    expect(await invoke("state", {})).toEqual({ active: false });
    foreignDialog = "hidden";
    expect(await invoke("start", { target: "project-create" })).toMatchObject({
      active: true,
      step: "open",
    });
  });
});

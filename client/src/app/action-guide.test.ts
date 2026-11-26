import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { workspacePages } from "@/contracts/pages";
import {
  fillAutomationField,
  formState,
  liveControls,
  submitAutomationForm,
} from "@/shared/desktop/automation-controls";
import {
  bindGuideForm,
  closeActionGuide,
  noteGuideFormOpen,
} from "@/shared/tutorial/action-guide-state";
import contract from "../../../server/internal/automation/commands.json";
import { guideAction, guideHandlers } from "./action-guide";

vi.mock("./automation-navigation", () => ({
  awaitUI: async (predicate: () => boolean) => {
    if (!predicate()) throw Error("AUTOMATION_TARGET_UNAVAILABLE");
  },
  visibleDialog: () => false,
  navigateAutomationPage: vi.fn(),
}));
const invoke = (action: string, target: string, field = "", owner = "client") =>
  guideAction({ action, target, field }, workspacePages, owner, "Example client");
const input = vi.fn(),
  save = vi.fn(),
  cancel = vi.fn(),
  open = vi.fn();
const fakeNode = (attrs: Record<string, string> = {}) => ({
  getClientRects: () => [{}],
  closest: () => null,
  getAttribute: (name: string) => attrs[name],
  hasAttribute: (name: string) => Object.hasOwn(attrs, name),
  scrollIntoView: vi.fn(),
  querySelectorAll: (selector: string) => nodes[selector] || [],
});
let nodes: Record<string, ReturnType<typeof fakeNode>[]>;
beforeEach(() => {
  nodes = Object.fromEntries(
    Object.entries({
      "data-ops-guide-open": "project.create",
      "data-ops-guide-command": "submit",
      "data-ops-guide-form": "project.create",
      "data-ops-guide-field": "name",
      "data-ops-guide-control": "home.search",
      "data-ops-action-card": "",
    }).map(([attr, value]) => [`[${attr}]`, [fakeNode({ [attr]: value })]]),
  );
  vi.stubGlobal("document", { querySelectorAll: (selector: string) => nodes[selector] || [] });
  vi.stubGlobal("getComputedStyle", () => ({ visibility: "visible" }));
  vi.stubGlobal("window", { location: { href: "http://localhost/", pathname: "/" } });
  liveControls.openers.set("project.create", open);
  liveControls.scopes.set("projects", () => ({ target: "", tab: "" }));
  liveControls.scopes.set("home", () => ({
    target: "",
    tab: "",
    controls: { search: { run: input } },
  }));
});
afterEach(() => {
  closeActionGuide();
  liveControls.openers.clear();
  liveControls.forms.clear();
  liveControls.scopes.clear();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});
describe("操作引导", () => {
  it("正式合同中每个可引导动作都有真实处理器", () => {
    expect(Object.keys(guideHandlers).sort()).toEqual(
      contract.commands
        .filter((item) => item.guide)
        .map((item) => item.name)
        .sort(),
    );
  });
  it("只定位已注册的控件，控件未挂载时明确失败", async () => {
    expect(await invoke("page_control", "home", "search")).toMatchObject({
      located: true,
      executed: false,
    });
    expect(input).not.toHaveBeenCalled();
    delete nodes["[data-ops-guide-control]"];
    await expect(invoke("page_control", "home", "search")).rejects.toThrow(
      "AUTOMATION_TARGET_UNAVAILABLE",
    );
    await expect(invoke("page_control", "home", "unknown")).rejects.toThrow(
      "AUTOMATION_FIELD_UNAVAILABLE",
    );
  });
  it("来源表单直接引导到来源选择框，定位时不打开编辑会话", async () => {
    const target = "application.source";
    liveControls.openers.set(target, open);
    nodes["[data-ops-guide-open]"] = [
      fakeNode({ "data-ops-guide-open": target, "data-ops-guide-field": "source.provider" }),
    ];
    expect(await invoke("form_open", target)).toMatchObject({
      located: true,
      executed: false,
      hint: "inlineForm",
    });
    expect(open).not.toHaveBeenCalled();
  });
  it("用户主动打开后才能继续指引该表单，多客户端和重新打开的草稿彼此隔离", async () => {
    const form = () => ({
      target: "project.create",
      values: { name: "example" },
      fields: { name: input },
      close: cancel,
      submit: save,
    });
    await invoke("form_open", "project.create");
    expect(open).not.toHaveBeenCalled();
    await expect(invoke("form_open", "project.create", "", "other")).rejects.toThrow(
      "AUTOMATION_TUTORIAL_OWNED",
    );
    noteGuideFormOpen("project.create");
    liveControls.forms.set("project.create", form);
    bindGuideForm("project.create", form);
    const response = await invoke("form_fill", "project.create", "name");
    expect(response).toMatchObject({ located: true, executed: false });
    expect(response).not.toHaveProperty("values");
    await invoke("form_submit", "project.create");
    expect([input.mock.calls, save.mock.calls, cancel.mock.calls]).toEqual([[], [], []]);
    closeActionGuide();
    await expect(invoke("form_fill", "project.create", "name", "other")).rejects.toThrow(
      "AUTOMATION_DRAFT_ACTIVE",
    );
    liveControls.forms.set("project.create", () => form());
    await expect(invoke("form_fill", "project.create", "name")).rejects.toThrow(
      "AUTOMATION_DRAFT_ACTIVE",
    );
  });
  it("按服务器实例引导人工专填字段，状态只公开字段名和授权范围内的表单值", async () => {
    const id = "server.jenkins.bind",
      target = `${id}/server-sample`;
    const read = () => ({
      target,
      values: { domainId: "domain-sample" },
      fields: { domainId: input },
      manualFields: ["viewerPassword"],
      close: cancel,
      submit: save,
    });
    liveControls.forms.set(id, read);
    nodes["[data-ops-guide-form]"] = [fakeNode({ "data-ops-guide-form": id })];
    nodes["[data-ops-guide-field]"] = [fakeNode({ "data-ops-guide-field": "viewerPassword" })];
    const editing = { client: "sample", target, read };
    expect(
      await guideAction(
        { action: "form_fill", target, field: "viewerPassword" },
        workspacePages,
        "sample",
        "Client",
        editing,
      ),
    ).toMatchObject({ located: true, executed: false, hint: "manualPassword" });
    expect(formState(target).forms[0]).toEqual({
      id,
      target,
      fields: ["domainId"],
      manualFields: ["viewerPassword"],
      values: { domainId: "domain-sample" },
    });
    expect(formState().forms[0].values).toBeUndefined();
    closeActionGuide();
    await expect(
      guideAction(
        { action: "form_fill", target: `${id}/other`, field: "viewerPassword" },
        workspacePages,
        "sample",
        "Client",
        editing,
      ),
    ).rejects.toThrow("AUTOMATION_DRAFT_ACTIVE");
  });
  it("Jenkins 表单只接受登记字段，提交失败和无效回执均可观察，成功只返回操作 ID 和状态", async () => {
    const submit = vi.fn();
    const form = {
      target: "server.jenkins.bind/server-sample",
      values: {},
      fields: { domainId: input },
      manualFields: ["viewerPassword"],
      operationReceipt: true,
      close: cancel,
      submit,
    };
    expect(() => fillAutomationField(form, "viewerPassword", "example")).toThrow(
      "AUTOMATION_MANUAL_INPUT_REQUIRED",
    );
    expect(input).not.toHaveBeenCalled();
    fillAutomationField(form, "domainId", "domain-sample");
    expect(input).toHaveBeenCalledWith("domain-sample");
    submit
      .mockRejectedValueOnce(Error("AUTOMATION_MANUAL_INPUT_REQUIRED"))
      .mockRejectedValueOnce(Error("EXAMPLE_FAILED"))
      .mockResolvedValueOnce({ status: "queued" })
      .mockResolvedValueOnce({ id: "run-sample", status: "queued", internal: "example" });
    await expect(submitAutomationForm(form)).rejects.toThrow("AUTOMATION_MANUAL_INPUT_REQUIRED");
    await expect(submitAutomationForm(form)).rejects.toThrow("EXAMPLE_FAILED");
    await expect(submitAutomationForm(form)).rejects.toThrow("AUTOMATION_OPERATION_UNVERIFIED");
    await expect(submitAutomationForm(form)).resolves.toEqual({
      operationRunId: "run-sample",
      status: "queued",
    });
  });
});

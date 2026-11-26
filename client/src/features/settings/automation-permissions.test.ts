import { describe, expect, it } from "vitest";
import {
  type Client,
  changePermissionLevel,
  emptyPermission,
  overlappingTargets,
  permissionLevel,
  policyDraft,
  policyPayload,
} from "./automation-permissions";

const command = "application_release";
const exact = (targets: string, exceptions = "") => ({ limited: true, targets, exceptions });
const base = { id: "example", name: "示例客户端" };
describe("本地 MCP 权限", () => {
  it("新客户端默认仅能查看目录，保存时显式声明模式", () => {
    expect(policyPayload(policyDraft())).toEqual({
      permissions: [{ command: "command_catalog", mode: "direct", targets: [] }],
      rules: [],
      valid: true,
    });
    expect(policyPayload(policyDraft({ ...base, permissions: [], rules: [] })).permissions).toEqual(
      [],
    );
  });
  it("缺少模式的旧授权按原来的直接权限回读", () => {
    const client = {
      ...base,
      permissions: [{ command, targets: ["app_a", "app_b"] }],
      rules: [{ command, target: "app_b" }],
    };
    expect(policyPayload(policyDraft(client))).toEqual({
      permissions: [{ command, mode: "direct", targets: ["app_a", "app_b"] }],
      rules: client.rules,
      valid: true,
    });
  });
  it("同一动作可分别为两个对象保存引导及直接权限，只有直接目标可免确认", () => {
    const client: Client = {
      ...base,
      permissions: [
        { command, mode: "guide", targets: ["app_a"] },
        { command, mode: "direct", targets: ["app_b"] },
      ],
      rules: [{ command, target: "app_b" }],
    };
    const draft = policyDraft(client);
    expect(permissionLevel(draft[command])).toBe("mixed");
    expect(draft[command].guide?.exceptions).toBe("");
    expect(policyPayload(draft)).toEqual({
      permissions: client.permissions,
      rules: client.rules,
      valid: true,
    });
    expect(policyPayload({ [command]: { guide: exact("app_a", "app_a") } }).rules).toEqual([]);
  });
  it("切换模式保留原来的精确范围，移除操作权限同时移除免确认规则", () => {
    const first = { [command]: { direct: exact("app_b", "app_b") } };
    const mixed = changePermissionLevel(first, command, "mixed");
    expect(mixed[command].guide).toEqual(exact(""));
    expect(policyPayload(mixed).valid).toBe(false);
    const guide = changePermissionLevel(first, command, "guide");
    expect(guide[command].guide).toEqual(exact("app_b"));
    expect(policyPayload(guide).rules).toEqual([]);
    expect(changePermissionLevel(guide, command, "disabled")).toEqual({});
  });
  it("相交目标同时具备直接权限，全部直接操作覆盖全部引导目标", () => {
    expect(overlappingTargets({ guide: exact("a"), direct: exact("b") })).toEqual([]);
    expect(overlappingTargets({ guide: exact("a\nb"), direct: exact("b\nc") })).toEqual(["b"]);
    expect(overlappingTargets({ guide: emptyPermission, direct: exact("a") })).toEqual(["a"]);
    expect(overlappingTargets({ guide: exact("a"), direct: emptyPermission })).toBe("all");
  });
  it("目标上限、精确值与免确认范围按模式分别校验", () => {
    for (const targets of ["", "*", "项".repeat(54)]) {
      expect(policyPayload({ [command]: { guide: exact(targets) } }).valid).toBe(false);
      expect(policyPayload({ [command]: { direct: exact(targets) } }).valid).toBe(false);
    }
    expect(policyPayload({ [command]: { guide: exact("a"), direct: exact("b", "a") } }).valid).toBe(
      false,
    );
  });
  it("派生引导工具不会作为独立授权保存", () => {
    const draft = policyDraft({
      ...base,
      permissions: [{ command: "action_guide", targets: [] }],
      rules: [],
    });
    expect(draft).toEqual({});
    expect(policyPayload({ action_guide: { direct: emptyPermission } }).permissions).toEqual([]);
  });
});

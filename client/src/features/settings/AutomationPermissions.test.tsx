import { IqujiOverlayProvider } from "@iquji-community/web-component";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { commands } from "../../../../server/internal/automation/commands.json";
import { AutomationPermissions } from "./AutomationPermissions";
import {
  type Client,
  describeScopeSummary,
  policyDraft,
  summarizeDraft,
} from "./automation-permissions";
import { McpScopeDialog } from "./McpScopeDialog";

vi.mock("@iquji-community/web-component", async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>();
  return {
    ...actual,
    IqujiDialog: ({
      open,
      title,
      children,
      actions,
    }: {
      open: boolean;
      title?: ReactNode;
      children?: ReactNode;
      actions?: ReactNode;
    }) =>
      open ? (
        <div data-testid="iquji-dialog">
          <h2>{title}</h2>
          <div>{children}</div>
          <div>{actions}</div>
        </div>
      ) : null,
  };
});

const client: Client = {
  id: "demo",
  name: "示例",
  permissions: [
    { command: "application_release", mode: "guide", targets: ["app_a"] },
    { command: "application_release", mode: "direct", targets: ["app_b"] },
    { command: "tutorial_goto", mode: "direct", targets: ["workspace"] },
  ],
  rules: [{ command: "application_release", target: "app_b" }],
};

it("平整权限列表过滤 action_guide 并展示搜索、筛选器、范围按钮与确认标记", () => {
  const html = renderToStaticMarkup(
    <IqujiOverlayProvider>
      <AutomationPermissions
        commands={commands}
        disabled={false}
        draft={policyDraft(client)}
        setDraft={vi.fn()}
      />
    </IqujiOverlayProvider>,
  );
  expect(html).not.toContain(">action_guide<");
  expect(html).toContain(">tutorial_goto<");
  expect(html).toContain("搜索工具名称或标识...");
  expect(html).toContain("全部分组");
  expect(html).toContain("全部状态");
  expect(html).toContain("需确认");
  expect(html).toContain("范围");
  // 不应再有嵌套卡片中的内嵌 fieldset
  expect(html).not.toContain("<fieldset");
});

it("目标范围弹窗为引导与直接模式分别提供范围设置，免确认只出现在直接操作", () => {
  const draft = policyDraft(client);
  const releaseCmd = commands.find((cmd) => cmd.name === "application_release");
  expect(releaseCmd).toBeDefined();

  const html = renderToStaticMarkup(
    <IqujiOverlayProvider>
      <McpScopeDialog
        command={releaseCmd ?? null}
        disabled={false}
        onApply={vi.fn()}
        onClose={vi.fn()}
        scopes={draft.application_release}
      />
    </IqujiOverlayProvider>,
  );
  expect(html).toContain("仅引导范围");
  expect(html).toContain("直接操作范围");
  expect(html.match(/免确认目标 ID/g)).toHaveLength(1);
});

it("权限摘要辅助函数准确统计授权数与范围标签", () => {
  const draft = policyDraft(client);
  const summary = summarizeDraft(draft, commands);
  expect(summary.granted).toBe(2);
  expect(summary.direct).toBe(2);
  expect(summary.guide).toBe(1);

  const releaseCmd = commands.find((cmd) => cmd.name === "application_release");
  if (!releaseCmd) throw new Error("application_release command missing");
  const mockT = ((key: string, params?: Record<string, unknown>) =>
    params ? `${key}:${JSON.stringify(params)}` : key) as unknown as (key: string) => string;
  const scopeDesc = describeScopeSummary(draft.application_release, releaseCmd, mockT);
  expect(scopeDesc.invalid).toBe(false);
  expect(scopeDesc.label).toContain("automation.scopeLimitedSummary");
  expect(scopeDesc.label).toContain("automation.scopePreapprovedSummary");
});

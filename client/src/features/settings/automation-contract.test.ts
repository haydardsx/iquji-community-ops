import { expect, it } from "vitest";
import { enUSAutomation } from "@/shared/i18n/locales/en-US/automation";
import { zhCNAutomation } from "@/shared/i18n/locales/zh-CN/automation";
import { commands } from "../../../../server/internal/automation/commands.json";

it("设置为正式目录的每项权限展示双语名称、实际影响与分组", () => {
  for (const messages of [enUSAutomation, zhCNAutomation]) {
    const labels = Object.keys(messages)
      .filter((key) => key.startsWith("automation.label."))
      .map((key) => key.slice("automation.label.".length));
    expect(labels.sort()).toEqual(commands.map((cmd) => cmd.name).sort());
    for (const { name, group } of commands) {
      expect(messages).toHaveProperty([`automation.group.${group}`]);
      expect(messages).toHaveProperty([`automation.effect.${name}`], expect.stringMatching(/\S/));
    }
  }
});

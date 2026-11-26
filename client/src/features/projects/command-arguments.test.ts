import { describe, expect, it } from "vitest";
import { commandDraft, commandValue, shellScriptIndex } from "./command-arguments";
import { applicationFormSchema, configPayload, formValues } from "./model";

describe("命令编辑与执行协议", () => {
  const script = "printf '%s\\n' \"$1\"\nprintf '  %s' \"$2\"\n";
  it.each([
    { argv: ["sh", "-ec", script], scriptAt: 2 },
    { argv: ["/bin/bash", "-e", "-c", script, "", "a b", '"quoted"'], scriptAt: 3 },
    {
      argv: ["/bin/bash", "--noprofile", "-o", "pipefail", "-ec", script, "init", "a b"],
      scriptAt: 5,
    },
    { argv: ["sh", "-o", "errexit", "file.sh", "-c", "value"], scriptAt: undefined },
    { argv: ["CMD-SHELL", script], scriptAt: undefined },
    { argv: ["curl", "-fsS", "https://example.test/a b"], scriptAt: undefined },
    { argv: ["run", "-c", "", "--", "a\nb"], scriptAt: undefined },
    { argv: ["sh", "file.sh", "-c", "value"], scriptAt: undefined },
  ])("保留 $argv 的可执行文件、参数、脚本和位置", ({ argv, scriptAt }) => {
    expect(shellScriptIndex(argv)).toBe(scriptAt);
    const form = formValues({
      swarmRuntime: {
        command: argv,
        initializeCommand: argv,
        healthCommand: argv,
        logCommand: argv,
      },
    });
    const saved = configPayload(applicationFormSchema.parse(form));
    for (const key of ["command", "initializeCommand", "healthCommand", "logCommand"] as const)
      expect(saved.swarmRuntime?.[key]).toEqual(argv);
  });
  it("可选命令清空后不验证 JSON，检查参数个数、字节数和空字符", () => {
    const form = formValues({});
    for (const argv of [
      Array(129).fill("a"),
      ["serve", "a\0b"],
      ["serve", "汉".repeat(6000)],
      ["", "a"],
    ]) {
      form.healthCommand = commandDraft(argv);
      expect(applicationFormSchema.safeParse(form).success).toBe(false);
    }
    form.healthCommand.text = "";
    expect(
      configPayload(applicationFormSchema.parse(form)).swarmRuntime?.healthCommand,
    ).toBeUndefined();
  });
  it("直接命令的字面参数经引用后仍可逆，包括空值、换行和引号", () => {
    const argv = [
      "curl",
      "-fsS",
      "https://example.test/health",
      "a b",
      "",
      "x'y",
      "$literal",
      "a\nb",
    ];
    const draft = commandDraft(argv);
    expect(commandValue({ ...commandDraft(), text: draft.text }).argv).toEqual(argv);
    expect(commandDraft(argv.slice(0, 3)).text).toBe("curl -fsS https://example.test/health");
  });
  it("管道、变量、注释和换行调用解释器；编辑旧脚本保留选项和位置参数", () => {
    for (const text of [
      "echo ok | cat",
      'echo "$VALUE"',
      "# note\n  true\n",
      "pwd > result",
      "cd /tmp",
      "VAR=value run",
    ]) {
      expect(commandValue({ ...commandDraft(), text })).toEqual({
        argv: ["/bin/sh", "-c", text],
        shellEntrypoint: true,
      });
    }
    const draft = commandDraft(["/bin/bash", "-ec", "true", "", "a b"]);
    expect(commandValue({ ...draft, text: "  printf '%s' \"$1\"\n" }).argv).toEqual([
      "/bin/bash",
      "-ec",
      "  printf '%s' \"$1\"\n",
      "",
      "a b",
    ]);
  });
});

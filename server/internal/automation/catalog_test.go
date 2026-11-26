package automation

import (
	"slices"
	"testing"
)

// 使用隔离状态和不存在的目标逐条走业务分发，防止目录中出现没有执行入口的操作。
func TestCatalogAndBusinessDispatch(t *testing.T) {
	s := testService(t)
	client := Client{ID: "example"}
	seen := map[string]bool{}
	for _, cmd := range Catalog {
		if seen[cmd.Name] || cmd.Name == "" || cmd.Description == "" {
			t.Fatalf("命令重复或缺少说明：%s", cmd.Name)
		}
		seen[cmd.Name] = true
		if !slices.Contains([]string{"data", "logs", "operations", "window", "tutorial"}, cmd.Group) {
			t.Fatalf("命令分组无效：%s", cmd.Name)
		}
		windowOperation := cmd.Group == "window" && cmd.Name != "page_state" && cmd.Name != "action_guide"
		businessOperation := cmd.Group == "operations" && cmd.Name != "operation_get"
		if (cmd.Confirm || windowOperation || businessOperation) && !cmd.Guide {
			t.Fatalf("操作没有提供仅引导授权：%s", cmd.Name)
		}
		args := map[string]string{}
		for _, field := range cmd.Fields {
			if _, duplicate := args[field]; field == "" || duplicate {
				t.Fatalf("参数缺失或重复：%s.%s", cmd.Name, field)
			}
			args[field] = "example"
		}
		for _, field := range cmd.Required {
			if !slices.Contains(cmd.Fields, field) {
				t.Fatalf("必填参数未登记：%s.%s", cmd.Name, field)
			}
		}
		if cmd.Name == GuideCommand {
			args = map[string]string{"action": "application_release", "target": "example"}
		}
		call := Call{cmd.Name, args}
		if _, err := call.Validate(); err != nil {
			t.Fatalf("%s：%v", cmd.Name, err)
		}
		if cmd.usesWindow() {
			continue
		}
		if _, err := s.business(t.Context(), client, call); err != nil && err.Error() == "AUTOMATION_COMMAND_UNKNOWN" {
			t.Fatalf("业务动作缺少执行入口：%s", cmd.Name)
		}
	}
}

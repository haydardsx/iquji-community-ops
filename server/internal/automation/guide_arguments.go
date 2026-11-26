package automation

import (
	"errors"
	"slices"
	"strings"
)

var errArgumentRequired = errors.New("AUTOMATION_ARGUMENT_REQUIRED")

// GuideField 仅允许原动作与引导命令都声明的上下文，业务提交数据不能随引导传递。
func (cmd Command) GuideField(field string) bool {
	guide, ok := Lookup(GuideCommand)
	return ok && cmd.Guide && slices.Contains(cmd.Fields, field) && slices.Contains(guide.Fields, field)
}

// 引导仅保留定位所需参数。应用日志可先定位到服务选择器，再由用户选择槽位。
func (cmd Command) GuideRequired() []string {
	fields := []string{"action"}
	for _, field := range cmd.Required {
		if cmd.GuideField(field) && !(cmd.Name == "application_logs" && field == "slot") {
			fields = append(fields, field)
		}
	}
	return fields
}

// 具体日志副本属于一个服务槽位，指定副本时必须同时定位槽位。
func (cmd Command) GuideDependencies() map[string][]string {
	if cmd.Name == "application_logs" {
		return map[string][]string{"task": {"slot"}}
	}
	return nil
}

func (call Call) validateGuide() error {
	cmd, ok := Lookup(call.Args["action"])
	if !ok || !cmd.Guide || cmd.Name == GuideCommand {
		return errors.New("AUTOMATION_ARGUMENT_INVALID")
	}
	for field := range call.Args {
		if field != "action" && !cmd.GuideField(field) {
			return errors.New("AUTOMATION_ARGUMENT_INVALID")
		}
	}
	required := cmd.GuideRequired()
	for key, fields := range cmd.GuideDependencies() {
		if _, present := call.Args[key]; present {
			required = append(required, fields...)
		}
	}
	for _, field := range required {
		if strings.TrimSpace(call.Args[field]) == "" {
			return errArgumentRequired
		}
	}
	return nil
}

// 可不带目标的导航在限定目标授权下仍须显式指定其中一个目标。
func (c Client) guideRequiresTarget(action string) bool {
	found := false
	for _, p := range c.Permissions {
		if p.Command == action {
			if len(p.Targets) == 0 {
				return false
			}
			found = true
		}
	}
	return found
}

package automation

import (
	"errors"
	"slices"
	"strings"
	"unicode"
)

const (
	ModeDirect   = "direct"
	ModeGuide    = "guide"
	GuideCommand = "action_guide"
)

// Permission 按权限模式限定命令的目标，显式空列表表示该模式覆盖全部目标。
type Permission struct {
	Command string   `json:"command"`
	Targets []string `json:"targets"`
	Mode    string   `json:"mode"`
}

var errInvalidGrant = errors.New("AUTOMATION_GRANT_INVALID")

// 此清单固定为初版发布时的命令，仅用于升级旧客户端，后续命令须单独授权。
func legacyPermissions() []Permission {
	names := []string{
		"command_catalog", "monitoring_summary", "projects_list", "servers_list", "tunnels_list", "templates_list",
		"application_runtime", "container_logs", "application_log_configure", "application_logs", "application_release",
		"application_restart", "operation_get", "page_state", "page_control", "page_open", "form_open", "form_close", "form_fill", "form_submit",
	}
	out := make([]Permission, 0, len(names))
	for _, name := range names {
		out = append(out, Permission{Command: name, Targets: []string{}, Mode: ModeDirect})
	}
	return out
}

func (c Client) Permits(call Call) bool {
	action := call.Name
	guide := action == GuideCommand
	if guide {
		action = call.Args["action"]
		cmd, ok := Lookup(action)
		if !ok || !cmd.Guide || action == GuideCommand {
			return false
		}
		for field := range call.Args {
			if field != "action" && !cmd.GuideField(field) {
				return false
			}
		}
	}
	for _, p := range c.Permissions {
		if p.Command == action && (p.Mode == ModeDirect || guide && p.Mode == ModeGuide) &&
			(len(p.Targets) == 0 || slices.Contains(p.Targets, call.Args["target"])) {
			return true
		}
	}
	return false
}

func (c Client) Preapproves(call Call) bool {
	for _, r := range c.Rules {
		if r.Command == call.Name && r.Target == call.Args["target"] {
			return true
		}
	}
	return false
}

func validTarget(target string) bool {
	return strings.TrimSpace(target) == target && target != "" && len(target) <= contract.Limits.MaxTargetBytes && !strings.Contains(target, "*") && strings.IndexFunc(target, unicode.IsControl) == -1
}

func makeClient(id, name string, permissions []Permission, rules []Rule) (Client, error) {
	c := Client{ID: id, Name: strings.TrimSpace(name), Permissions: permissions, Rules: rules}
	if c.Name == "" || len(c.Name) > contract.Limits.MaxClientNameBytes || permissions == nil || len(permissions) > 2*len(Catalog) || len(rules) > contract.Limits.MaxPreapprovals {
		return Client{}, errInvalidGrant
	}
	if c.Rules == nil {
		c.Rules = []Rule{}
	}
	seen := map[[2]string]bool{}
	for _, p := range permissions {
		cmd, ok := Lookup(p.Command)
		key := [2]string{p.Command, p.Mode}
		if !ok || p.Command == GuideCommand || seen[key] {
			return Client{}, errInvalidGrant
		}
		if p.Mode != ModeDirect && !(p.Mode == ModeGuide && cmd.Guide) {
			return Client{}, errInvalidGrant
		}
		if p.Targets == nil || len(p.Targets) > contract.Limits.MaxTargetsPerCommand || len(p.Targets) > 0 && !slices.Contains(cmd.Fields, "target") {
			return Client{}, errInvalidGrant
		}
		seen[key] = true
		targets := map[string]bool{}
		for _, target := range p.Targets {
			if !validTarget(target) || targets[target] {
				return Client{}, errInvalidGrant
			}
			targets[target] = true
		}
	}
	preapproved := map[Rule]bool{}
	for _, rule := range rules {
		cmd, ok := Lookup(rule.Command)
		if !ok || !cmd.Confirm || !validTarget(rule.Target) || preapproved[rule] || !c.Permits(Call{rule.Command, map[string]string{"target": rule.Target}}) {
			return Client{}, errInvalidGrant
		}
		preapproved[rule] = true
	}
	return c, nil
}

func (c Client) Directory() map[string]any {
	commands := []Command{}
	guides := []Permission{}
	direct := map[string]bool{}
	for _, p := range c.Permissions {
		if p.Mode == ModeDirect {
			direct[p.Command] = true
		}
		if cmd, ok := Lookup(p.Command); ok && cmd.Guide {
			guides = append(guides, p)
		}
	}
	for _, cmd := range Catalog {
		if direct[cmd.Name] || cmd.Name == GuideCommand && len(guides) > 0 {
			commands = append(commands, cmd)
		}
	}
	return map[string]any{"version": Version, "commands": commands, "permissions": c.Permissions, "guides": guides, "rules": c.Rules, "pages": Pages}
}

package automation

import (
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// 命令目录同时用于 MCP 工具发现、桌面授权和执行校验。
type Command struct {
	Group       string   `json:"group"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Fields      []string `json:"fields"`
	Required    []string `json:"required"`
	Confirm     bool     `json:"confirm"`
	Guide       bool     `json:"guide"`
}

type Page struct {
	Path string   `json:"path"`
	Tabs []string `json:"tabs"`
}

// 策略上限与页面、命令一同发布，设置页与执行端共享同一份约束。
type Limits struct {
	MaxClientNameBytes   int `json:"maxClientNameBytes"`
	MaxTargetsPerCommand int `json:"maxTargetsPerCommand"`
	MaxPreapprovals      int `json:"maxPreapprovals"`
	MaxTargetBytes       int `json:"maxTargetBytes"`
	MaxHintCharacters    int `json:"maxHintCharacters"`
	MaxArgumentBytes     int `json:"maxArgumentBytes"`
}
type Contract struct {
	Version  int             `json:"version"`
	Limits   Limits          `json:"limits"`
	Pages    map[string]Page `json:"pages"`
	Commands []Command       `json:"commands"`
}

//go:embed commands.json
var contractJSON []byte

var contract = loadContract()
var Version, Pages, Catalog = contract.Version, contract.Pages, contract.Commands

func loadContract() Contract {
	var descriptor Contract
	if err := json.Unmarshal(contractJSON, &descriptor); err != nil {
		panic(err)
	}
	return descriptor
}
func Directory() Contract { return contract }

func (c Command) usesWindow() bool { return c.Group == "window" || c.Group == "tutorial" }

func Lookup(name string) (Command, bool) {
	for _, cmd := range Catalog {
		if cmd.Name == name {
			return cmd, true
		}
	}
	return Command{}, false
}

type Call struct {
	Name string            `json:"name"`
	Args map[string]string `json:"arguments"`
}

func (c Call) Validate() (Command, error) {
	cmd, ok := Lookup(c.Name)
	if !ok {
		return cmd, errors.New("AUTOMATION_COMMAND_UNKNOWN")
	}
	if c.Name == "tutorial_hint" && utf8.RuneCountInString(c.Args["message"]) > contract.Limits.MaxHintCharacters {
		return cmd, errors.New("AUTOMATION_ARGUMENT_INVALID")
	}
	for k, v := range c.Args {
		if !slices.Contains(cmd.Fields, k) || len(v) > contract.Limits.MaxArgumentBytes || strings.ContainsRune(v, 0) {
			return cmd, errors.New("AUTOMATION_ARGUMENT_INVALID")
		}
	}
	for _, k := range cmd.Required {
		if strings.TrimSpace(c.Args[k]) == "" {
			return cmd, errors.New("AUTOMATION_ARGUMENT_REQUIRED")
		}
	}
	if c.Name == GuideCommand {
		return cmd, c.validateGuide()
	}
	return cmd, nil
}

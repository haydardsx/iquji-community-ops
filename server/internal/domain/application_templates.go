package domain

import (
	"embed"
	"encoding/json"
	"slices"
	"sync"
)

//go:embed templates/*.json
var templateFiles embed.FS

var templateSeeds = sync.OnceValue(func() []Object {
	entries, err := templateFiles.ReadDir("templates")
	if err != nil {
		panic(err)
	}
	items := make([]Object, 0, len(entries))
	for _, entry := range entries {
		data, err := templateFiles.ReadFile("templates/" + entry.Name())
		if err != nil {
			panic(err)
		}
		var item Object
		if err := json.Unmarshal(data, &item); err != nil {
			panic(err)
		}
		items = append(items, item)
	}
	return items
})

// 内置配置只在首次安装、旧状态升级或恢复出厂时复制，实际修改保存在用户目录中。
func DefaultApplicationTemplates() []Object {
	return CloneObjectSliceWithoutUserReferences(templateSeeds())
}

const applicationTemplateSeedVersion = 2

// 初版之后新增的内置模板按首次引入版本补入用户目录。
var templateIntroducedIn = map[string]int{"pmail": 1, "demo-backend": 2, "demo-frontend": 2}

func (state *State) ensureApplicationTemplates() {
	if state.ApplicationTemplates == nil {
		state.ApplicationTemplates = DefaultApplicationTemplates()
	}
	if state.ApplicationTemplateSeedVersion >= applicationTemplateSeedVersion {
		return
	}
	// 空集合表示用户已清空模板库。同 ID 的本地模板始终保留。
	if len(state.ApplicationTemplates) > 0 {
		for _, seed := range templateSeeds() {
			id := StringValue(seed["id"])
			if templateIntroducedIn[id] > state.ApplicationTemplateSeedVersion && !slices.ContainsFunc(state.ApplicationTemplates, func(item Object) bool { return item["id"] == id }) {
				state.ApplicationTemplates = append(state.ApplicationTemplates, CloneObjectWithoutUserReferences(seed))
			}
		}
	}
	state.ApplicationTemplateSeedVersion = applicationTemplateSeedVersion
}

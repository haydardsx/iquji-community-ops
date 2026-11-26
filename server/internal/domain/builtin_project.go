package domain

const BuiltinProjectID = "project_builtin"

// EnsureBuiltinProject 以稳定标识恢复基础服务容器；应用由使用者自行创建。
func EnsureBuiltinProject(state *State) {
	project := FindObjectBy(state.Projects, "id", BuiltinProjectID)
	if project == nil {
		project = Object{"id": BuiltinProjectID, "tags": []string{}, "serverIds": []string{}, "applicationIds": []string{}}
		state.Projects = append(state.Projects, project)
	}
	project["name"], project["builtin"] = "基础服务", true
}

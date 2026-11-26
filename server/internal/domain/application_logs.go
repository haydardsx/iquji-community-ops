package domain

// MigrateApplicationLogConfiguration 删除从未参与实际读取的手工日志配置。
// 已有服务继续通过其真实 Docker logging driver 读取标准输出与错误输出。
func MigrateApplicationLogConfiguration(state *State) {
	for _, config := range state.ApplicationDeploymentConfigs {
		delete(config, "runtimeLogConfig")
	}
	for _, task := range state.Tasks {
		delete(ObjectFromAny(task.Payload["runtimeConfigSnapshot"]), "runtimeLogConfig")
	}
	for _, plan := range state.ReleasePlans {
		delete(ObjectFromAny(plan["buildSnapshot"]), "runtimeLogConfig")
	}
}

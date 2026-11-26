package domain

// JenkinsAccessSnapshots 返回仍占用网关和域名的生效、待发布及待回收入口。
func JenkinsAccessSnapshots(server Object) []Object {
	access := ObjectFromAny(server["jenkinsAccess"])
	var result []Object
	for _, field := range []string{"applied", "pending", "retiring"} {
		if snapshot := ObjectFromAny(access[field]); len(snapshot) > 0 {
			result = append(result, snapshot)
		}
	}
	return result
}

package domain

// 连接策略属于服务器资产，旧服务器在读取及恢复时补齐相同的直连配置。
func NormalizeServerConnections(servers []Object) {
	for _, server := range servers {
		if server != nil && server["connectionProxy"] == nil {
			server["connectionProxy"] = Object{"mode": "direct"}
		}
	}
}

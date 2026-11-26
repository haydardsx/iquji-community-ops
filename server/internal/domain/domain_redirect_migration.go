package domain

// 旧开关跟随各应用路由发布。迁移只生成域名草稿，远端快照继续记录旧文件的事实。
func MigrateDomainRedirects(state *State) {
	for _, asset := range state.Domains {
		enabled, exists := asset["httpsRedirectEnabled"]
		if !exists {
			continue
		}
		delete(asset, "httpsRedirectEnabled")
		if enabled != true || asset["redirect"] != nil {
			continue
		}
		certificate := FindObject(state.Certificates, StringValue(asset["certificateId"]))
		resolver := FindObject(state.Resolvers, StringValue(certificate["resolverId"]))
		ingress := FindObject(state.Ingresses, StringValue(resolver["ingressId"]))
		servers := StringSliceFromAny(ingress["serverIds"])
		gateway := ""
		if len(servers) == 1 {
			gateway = servers[0]
		}
		asset["redirect"] = Object{"targetDomainId": asset["id"], "gatewayServerId": gateway, "scheme": "https", "mode": "permanent"}
		asset["redirectRuntime"] = Object{"status": "pending", "legacyUpgrade": true}
	}
	for _, route := range state.Routes {
		bindings := ObjectSliceFromAny(route["domains"])
		for _, asset := range bindings {
			if _, exists := asset["httpsRedirectEnabled"]; !exists {
				continue
			}
			bindings = CloneObjectSlice(bindings)
			for _, bound := range bindings {
				delete(bound, "httpsRedirectEnabled")
			}
			route["domains"] = bindings
			break
		}
	}
}

package application

import (
	"context"
	"errors"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

func findDeleteTarget(state domain.State, targetKind string, targetID string) domain.Object {
	switch targetKind {
	case "project":
		return findObject(state.Projects, targetID)
	case "application":
		return findObject(state.Applications, targetID)
	case "release_plan":
		return findObject(state.ReleasePlans, targetID)
	case "server":
		return findObject(state.Servers, targetID)
	case "secret_asset":
		return findObject(state.SecretAssets, targetID)
	case "route":
		return findObject(state.Routes, targetID)
	case "domain":
		return findObject(state.Domains, targetID)
	case "certificate":
		return findObject(state.Certificates, targetID)
	case "resolver":
		return findObject(state.Resolvers, targetID)
	case "ingress":
		return findObject(state.Ingresses, targetID)
	case "frp_tunnel":
		if tunnel := domain.FindFrpTunnel(state.FrpTunnels, targetID); tunnel != nil {
			value, _ := domain.EncodeObject(*tunnel)
			return value
		}
		return nil
	default:
		return nil
	}
}

func deleteImpactKey(targetKind string) string {
	switch targetKind {
	case "project":
		return "projects"
	case "application":
		return "applications"
	case "release_plan":
		return "releasePlans"
	case "server":
		return "servers"
	case "secret_asset":
		return "secretAssets"
	case "route":
		return "routes"
	case "domain":
		return "domains"
	case "certificate":
		return "certificates"
	case "resolver":
		return "resolvers"
	case "ingress":
		return "ingresses"
	case "frp_tunnel":
		return "frpTunnels"
	default:
		return ""
	}
}

func secretSummary(assetType string, value string, fingerprint string) string {
	suffix := fingerprint
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	if suffix == "" {
		suffix = "unknown"
	}
	switch assetType {
	case "ssh_key":
		return "SSH key **** " + suffix
	case "application_secret", "registry_secret":
		return "********"
	case "git_token":
		return "Git token **** " + suffix
	default:
		if value == "" {
			return "Secret **** " + suffix
		}
		if len(value) <= 4 {
			return "Secret **** " + suffix
		}
		return "Secret ****" + value[len(value)-4:] + " " + suffix
	}
}

func validateSecretAssetMaterial(assetType string, value string) error {
	if assetType != "ssh_key" || strings.TrimSpace(value) == "" {
		return nil
	}
	if _, err := ssh.ParsePrivateKey([]byte(value)); err != nil {
		return errors.New("SSH_PRIVATE_KEY_INVALID: " + err.Error())
	}
	return nil
}

func (r *Runtime) publicSecretAsset(ctx context.Context, asset domain.Object) domain.Object {
	result := cloneObject(asset)
	summary := stringValue(result["maskedSummary"])
	if summary == "" || len(summary) > 64 || strings.Count(summary, "*") > 24 {
		result["maskedSummary"] = secretSummary(stringValue(result["assetType"]), "", stringValue(result["fingerprint"]))
	}
	available, err := r.Store.HasSecretMaterial(ctx, stringValue(result["secretRef"]))
	if err == nil && available {
		result["materialStatus"] = "available"
	} else if stringValue(result["pendingCreationOperationId"]) != "" {
		result["materialStatus"] = "pending"
	} else {
		result["materialStatus"] = "missing"
	}
	delete(result, "pendingCreationOperationId")
	delete(result, "appliedOperationRunId")
	delete(result, "revisionSecretRefs")
	delete(result, "materialRef")
	if result["assetType"] == "application_secret" || result["assetType"] == "registry_secret" {
		delete(result, "fingerprint")
	}
	return result
}

func shortHash(value string) string {
	hash := domain.HashSecret(value)
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func defaultServerPlatformProfile() domain.Object {
	return domain.Object{
		"osId":             "unknown",
		"osVersion":        "unknown",
		"architecture":     "x86_64",
		"packageManager":   "unknown",
		"serviceManager":   "systemd",
		"overrideSource":   "manual",
		"dockerRepoMirror": "auto",
	}
}

func enrichServer(server domain.Object, state domain.State) domain.Object {
	return enrichServerWithSnapshot(server, state, latestSwarmSnapshots(state)[stringValue(server["id"])])
}

func enrichServerWithSnapshot(server domain.Object, state domain.State, snapshot domain.Object) domain.Object {
	result := serverWithPublicMirror(server)
	id := stringValue(server["id"])
	agent := findObjectBy(state.MonitoringAgents, "serverId", id)
	health := domain.Object{
		"status":  firstNonEmptyString(stringValue(server["status"]), "idle"),
		"summary": "Server metadata is available",
		"metrics": map[string]any{
			"ssh":     false,
			"docker":  false,
			"swarm":   false,
			"traefik": false,
			"jenkins": false,
			"agent":   hasMonitoringAgent(state.MonitoringAgents, id),
		},
	}
	for key, value := range objectMapFromAny(server["health"]) {
		health[key] = value
	}
	metrics := observedServerMetrics(health, snapshot)
	activeTunnel := domain.ActiveFrpTunnelRunForServer(state.FrpTunnelRuns, id, "")
	metrics["frpsInUse"] = activeTunnel != nil
	metrics["frpsIdle"] = metrics["frpsPresent"] == true && (activeTunnel == nil || activeTunnel.Status == domain.FrpTunnelStatusCleanupRequired)
	health["metrics"] = metrics
	if stringValue(health["checkedAt"]) != "" {
		health["checks"] = normalizedOptionalRuntimeChecks(health)
		report := serverSnapshotReport(server, health)
		health["status"] = report.Status
		if report.Decision.Allowed {
			health["summary"] = "最近一次运行时探测已完成"
		}
	}
	result["health"] = health
	if agent == nil {
		result["agent"] = nil
	} else {
		result["agent"] = agent
	}
	result["connectionState"] = firstNonEmptyString(stringValue(server["connectionState"]), "unverified")
	result["runtimeSelectable"] = true
	result["selectionBlockedReasons"] = []string{}
	if _, ok := result["labels"]; !ok {
		result["labels"] = []string{}
	}
	if _, ok := result["projectIds"]; !ok {
		result["projectIds"] = []string{}
	}
	if _, ok := result["secretAssetIds"]; !ok {
		result["secretAssetIds"] = []string{}
	}
	if _, ok := result["platformProfile"]; !ok {
		result["platformProfile"] = defaultServerPlatformProfile()
	}
	return result
}

func serverWithPublicMirror(server domain.Object) domain.Object {
	result := cloneObject(server)
	if _, err := desiredDockerRegistryMirrorURL(server); err != nil {
		profile := cloneObject(objectMapFromAny(result["platformProfile"]))
		delete(profile, "dockerRegistryMirrorUrl")
		result["platformProfile"] = profile
	}
	return result
}

func applicationDetailFromState(state domain.State, applicationID string) domain.Object {
	application := findObject(state.Applications, applicationID)
	if application == nil {
		return nil
	}
	result := applicationWithInputStatus(state, application)
	projectID := stringValue(application["projectId"])
	result["project"] = findObject(state.Projects, projectID)
	config := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
	if config == nil {
		config = defaultDeploymentConfig(applicationID, projectID, []string{})
	}
	result["deploymentConfig"] = config
	result["applicationInstances"] = filterObjectsBy(state.ApplicationInstances, "applicationId", applicationID)
	routes := filterObjectsBy(state.Routes, "applicationId", applicationID)
	result["routes"] = routes
	result["certificates"] = certificatesForRoutes(state, routes)
	result["domains"] = domainsForRoutes(routes)
	result["deploymentNodes"] = placementServerIDs(config)
	return result
}

func domainsForRoutes(routes []domain.Object) []domain.Object {
	seen := map[string]bool{}
	result := []domain.Object{}
	for _, route := range routes {
		for _, domainItem := range objectSlice(route["domains"]) {
			id := stringValue(domainItem["id"])
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			result = append(result, domainItem)
		}
	}
	return result
}

func certificatesForRoutes(state domain.State, routes []domain.Object) []domain.Object {
	seen := map[string]bool{}
	result := []domain.Object{}
	for _, domainItem := range domainsForRoutes(routes) {
		certificateID := stringValue(domainItem["certificateId"])
		if certificateID == "" || seen[certificateID] {
			continue
		}
		if certificate := findObject(state.Certificates, certificateID); certificate != nil {
			seen[certificateID] = true
			result = append(result, certificate)
		}
	}
	return result
}

func normalizeRouteObject(item domain.Object, state *domain.State) domain.Object {
	application := findObject(state.Applications, stringValue(item["applicationId"]))
	item["projectId"] = application["projectId"]
	domains := []domain.Object{}
	for _, bound := range objectSlice(item["domains"]) {
		if asset := findObject(state.Domains, stringValue(bound["id"])); asset != nil {
			domains = append(domains, cloneObject(asset))
		}
	}
	item["domains"] = domains
	item["services"] = objectSlice(item["services"])
	item["rules"] = objectSlice(item["rules"])
	item["status"] = firstNonEmptyString(stringValue(item["status"]), "pending")
	item["accessibleUrls"] = routeAccessibleURLs(domains, objectSlice(item["rules"]))
	for _, key := range []string{"domainIds", "pathPrefix", "serviceName", "protocol", "service", "traefikLocator"} {
		delete(item, key)
	}
	return item
}

func routeAccessibleURLs(domains []domain.Object, rules []domain.Object) []string {
	result := []string{}
	for _, domainItem := range domains {
		host := stringValue(domainItem["name"])
		if host == "" {
			continue
		}
		for _, rule := range rules {
			path := firstNonEmptyString(stringValue(rule["path"]), "/")
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			scheme := "http"
			if stringValue(domainItem["certificateId"]) != "" {
				scheme = "https"
			}
			result = append(result, scheme+"://"+host+path)
		}
	}
	return result
}

func defaultDeploymentConfig(applicationID string, projectID string, serverIDs []string) domain.Object {
	return domain.Object{
		"id":                        domain.NewID("depconf"),
		"applicationId":             applicationID,
		"buildServerId":             "",
		"deploymentManagerServerId": "",
		"placement": domain.Object{
			"eligibleServerIds": append([]string{}, serverIDs...),
		},
		"environmentVariables": []domain.Object{},
		"deliveryConfig":       defaultDeliveryConfig(applicationID),
		"swarmRuntime":         defaultSwarmRuntime(applicationID, serverIDs),
		"buildConfig":          defaultBuildConfig(),
		"imageRetentionPolicy": domain.Object{"minRetainedImages": 2},
		"createdAt":            domain.Now(),
		"updatedAt":            domain.Now(),
	}
}

func defaultDeliveryConfig(applicationID string) domain.Object {
	_ = applicationID
	return domain.Object{
		"executor": "jenkins",
		"pipeline": domain.Object{
			"folder":      domain.ManagedJenkinsFolder,
			"jobName":     domain.ManagedJenkinsJob,
			"jobTemplate": domain.ManagedJenkinsJobTemplate,
			"triggerMode": "ops_manual",
		},
		"registrySync": domain.Object{"enabled": false},
	}
}

func defaultSwarmRuntime(applicationID string, serverIDs []string) domain.Object {
	serverID := firstString(serverIDs)
	serviceName := safeName(applicationID)
	return domain.Object{
		"ports":          []domain.Object{{"name": "web", "protocol": "http", "targetPort": 80}},
		"overlayNetwork": "iquops-public",
		"volumes":        []string{},
		"services": []domain.Object{{
			"slotKey":     "web",
			"serverId":    serverID,
			"serviceName": serviceName,
			"replicas":    1,
		}},
		"updateConfig":   domain.Object{"parallelism": 1, "order": "start-first", "failureAction": "rollback"},
		"rollbackConfig": domain.Object{"parallelism": 1, "order": "stop-first"},
	}
}

func defaultBuildConfig() domain.Object {
	return domain.Object{
		"environment":           "jenkins",
		"buildStrategy":         "image_build",
		"buildType":             "dockerfile",
		"toolchainRequirements": []domain.Object{},
		"dockerfileConfig":      domain.Object{"buildContextPath": ".", "dockerfilePath": "./Dockerfile", "buildArgsTemplate": map[string]string{}},
		"buildImagePolicy":      domain.Object{"cacheEnabled": true},
	}
}

func releasePlanBuildSnapshot(application domain.Object, config domain.Object) domain.Object {
	if isContainerSource(objectMapFromAny(config["sourceConfig"])) {
		return containerBuildSnapshot(config)
	}
	return domain.Object{
		"executor":             "jenkins",
		"builder":              "jenkins",
		"buildServerId":        stringValue(config["buildServerId"]),
		"deliveryConfig":       firstNonNil(config["deliveryConfig"], defaultDeliveryConfig(stringValue(application["id"]))),
		"swarmRuntime":         firstNonNil(config["swarmRuntime"], defaultSwarmRuntime(stringValue(application["id"]), placementServerIDs(config))),
		"buildConfig":          firstNonNil(config["buildConfig"], defaultBuildConfig()),
		"sourceSnapshot":       releaseSourceSnapshot(objectMapFromAny(config["sourceConfig"])),
		"imageRetentionPolicy": firstNonNil(config["imageRetentionPolicy"], domain.Object{"minRetainedImages": 2}),
	}
}

func releaseSourceSnapshot(source domain.Object) domain.Object {
	snapshot := cloneObject(source)
	if stringValue(snapshot["authSecretRef"]) != "" {
		snapshot["authMode"] = "git_token"
	}
	return snapshot
}

package application

import (
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type restoreObjectSet struct {
	kind       string
	items      []domain.Object
	target     func(*domain.State) *[]domain.Object
	naturalKey func(domain.Object) string
}

type restoreObjectPlan struct {
	templates   []domain.Object
	sets        []restoreObjectSet
	frpTunnels  []domain.FrpTunnel
	idMap       map[string]string
	outputError error
	secretMap   map[string]string
	conflicts   []domain.RestoreConflict
	selected    map[domain.BackupScope]bool
	selectedID  map[string]bool
}

func backupPayloadState(payload domain.BackupPayload, scopes []domain.BackupScope, includeLogsAndAudit bool) domain.State {
	state := domain.EmptyState()
	selected := backupScopeSet(scopes)
	if selected[domain.BackupScopeProjects] && payload.Projects != nil {
		state.ApplicationTemplates = domain.CloneObjectSliceWithoutUserReferences(payload.Projects.ApplicationTemplates)
		state.Projects = domain.CloneObjectSlice(payload.Projects.Projects)
		state.Applications = domain.CloneObjectSlice(payload.Projects.Applications)
		state.ApplicationDeploymentConfigs = domain.CloneObjectSlice(payload.Projects.ApplicationDeploymentConfigs)
		state.Routes = domain.CloneObjectSlice(payload.Projects.Routes)
	}
	if selected[domain.BackupScopeServers] && payload.Servers != nil {
		state.Servers = domain.CloneObjectSlice(payload.Servers.Servers)
		domain.NormalizeServerConnections(state.Servers)
		state.ServerRoles = domain.CloneObjectSlice(payload.Servers.ServerRoles)
	}
	if selected[domain.BackupScopeDomainsCertificates] && payload.DomainsCertificates != nil {
		state.Domains = domain.CloneObjectSlice(payload.DomainsCertificates.Domains)
		for _, asset := range state.Domains {
			delete(asset, "redirectRuntime")
		}
		state.Certificates = domain.CloneObjectSlice(payload.DomainsCertificates.Certificates)
		state.Resolvers = domain.CloneObjectSlice(payload.DomainsCertificates.Resolvers)
		state.Ingresses = domain.CloneObjectSlice(payload.DomainsCertificates.Ingresses)
	}
	if selected[domain.BackupScopeSecretAssets] && payload.SecretAssets != nil {
		state.SecretAssets = domain.CloneObjectSlice(payload.SecretAssets.Assets)
	}
	if selected[domain.BackupScopeFrpTunnels] && payload.FrpTunnels != nil {
		state.FrpTunnels = append([]domain.FrpTunnel(nil), payload.FrpTunnels.Tunnels...)
	}
	if includeLogsAndAudit && payload.LogsAndAudit != nil {
		state.RuntimeLogs = append([]domain.RuntimeLog(nil), payload.LogsAndAudit.RuntimeLogs...)
		state.OperationLogs = append([]domain.OperationLog(nil), payload.LogsAndAudit.OperationLogs...)
		for i := range state.RuntimeLogs {
			state.RuntimeLogs[i].Normalize()
		}
		for i := range state.OperationLogs {
			state.OperationLogs[i].Normalize()
		}
	}
	domain.MigrateApplicationSources(&state)
	domain.MigrateApplicationDeploymentLocations(&state)
	domain.MigrateRouteServiceWeights(&state)
	domain.MigrateDomainRedirects(&state)
	for _, application := range state.Applications {
		for _, field := range []string{"runtimeSnapshot", "deploymentLocation", "inputChanges", "appliedInputRevisions", "appliedInputSignatures", "appliedRuntimeKeys", "publishedBuildOutputs"} {
			delete(application, field)
		}
		application["status"] = "unverified"
	}
	for _, route := range state.Routes {
		delete(route, "lastApplied")
		delete(route, "pendingRemote")
		delete(route, "accessibleUrls")
		delete(route, "traefikLocator")
		route["status"] = "pending"
	}
	return state
}

func filterBackupPayload(payload domain.BackupPayload, scopes []domain.BackupScope, includeLogsAndAudit bool) domain.BackupPayload {
	selected := backupScopeSet(scopes)
	result := domain.BackupPayload{
		SchemaVersion:   payload.SchemaVersion,
		Scopes:          append([]domain.BackupScope(nil), scopes...),
		SecretMaterials: []domain.BackupSecretMaterial{},
	}
	if selected[domain.BackupScopeProjects] {
		result.Projects = payload.Projects
	}
	if selected[domain.BackupScopeServers] {
		result.Servers = payload.Servers
	}
	if selected[domain.BackupScopeDomainsCertificates] {
		result.DomainsCertificates = payload.DomainsCertificates
	}
	if selected[domain.BackupScopeSecretAssets] {
		result.SecretAssets = payload.SecretAssets
	}
	if selected[domain.BackupScopeFrpTunnels] {
		result.FrpTunnels = payload.FrpTunnels
	}
	if includeLogsAndAudit {
		result.LogsAndAudit = payload.LogsAndAudit
	}
	allowedRefs := map[string]bool{}
	for _, ref := range domain.BackupPayloadSecretRefs(result) {
		allowedRefs[ref] = true
	}
	for _, material := range payload.SecretMaterials {
		if allowedRefs[material.SecretRef] {
			result.SecretMaterials = append(result.SecretMaterials, material)
		}
	}
	return result
}

func buildRestoreObjectPlan(current domain.State, payload domain.BackupPayload, scopes []domain.BackupScope, uploadID string, stateDir string) restoreObjectPlan {
	incoming := backupPayloadState(payload, scopes, false)
	selected := backupScopeSet(scopes)
	plan := restoreObjectPlan{
		idMap:      map[string]string{},
		secretMap:  map[string]string{},
		conflicts:  []domain.RestoreConflict{},
		selected:   selected,
		selectedID: map[string]bool{},
	}
	plan.sets = restoreObjectSets(&incoming, selected)
	occupied := stateObjectIDs(current)

	for setIndex := range plan.sets {
		set := &plan.sets[setIndex]
		target := *set.target(&current)
		for itemIndex := range set.items {
			item := domain.CloneObject(set.items[itemIndex])
			sourceID := domain.StringValue(item["id"])
			if sourceID != "" {
				plan.selectedID[sourceID] = true
			}
			rewriteRestoreValue(item, plan.idMap, nil)
			if set.kind == "project" && sourceID == domain.BuiltinProjectID {
				plan.idMap[sourceID] = sourceID
				continue
			}
			key := set.naturalKey(item)
			if key != "" {
				if destination := findObjectByNaturalKey(target, set.naturalKey, key); destination != nil {
					destinationID := domain.StringValue(destination["id"])
					reasonCodes := []string{"NATURAL_KEY_CONFLICT"}
					if sourceID != "" && destinationID == sourceID {
						reasonCodes = append(reasonCodes, "ID_CONFLICT")
					}
					if sourceID != "" && destinationID != "" {
						plan.idMap[sourceID] = destinationID
					}
					plan.conflicts = append(plan.conflicts, domain.RestoreConflict{
						ID:            stableRestoreID("conflict", set.kind, sourceID, destinationID, key),
						Kind:          set.kind,
						SourceID:      sourceID,
						DestinationID: destinationID,
						NaturalKey:    key,
						ReasonCodes:   reasonCodes,
						Summary:       "目标中已存在相同自然键的" + set.kind,
					})
					continue
				}
			}
			if sourceID != "" && occupied[sourceID] {
				conflictKey := key
				if conflictKey == "" {
					conflictKey = "id=" + sourceID
				}
				plan.conflicts = append(plan.conflicts, domain.RestoreConflict{
					ID:            stableRestoreID("conflict", set.kind, sourceID, sourceID, conflictKey),
					Kind:          set.kind,
					SourceID:      sourceID,
					DestinationID: sourceID,
					NaturalKey:    conflictKey,
					ReasonCodes:   []string{"ID_CONFLICT"},
					Summary:       "目标中已存在相同 ID 的" + set.kind,
				})
			}
			if sourceID == "" || occupied[sourceID] {
				nextID := uniqueRestoredObjectID(set.kind, sourceID, uploadID, occupied)
				if sourceID != "" {
					plan.idMap[sourceID] = nextID
				}
				occupied[nextID] = true
			} else {
				occupied[sourceID] = true
			}
		}
	}

	for source, destination := range buildRestoreUploadAssetMap(current, incoming, plan.idMap, uploadID) {
		plan.idMap[source] = destination
	}
	for source, destination := range buildFilesystemUploadAssetMap(current, payload, plan.idMap, uploadID, stateDir) {
		plan.idMap[source] = destination
	}
	plan.secretMap = buildRestoreSecretRefMap(current, incoming, plan.idMap, uploadID)
	for setIndex := range plan.sets {
		for itemIndex := range plan.sets[setIndex].items {
			rewriteRestoreValue(plan.sets[setIndex].items[itemIndex], plan.idMap, plan.secretMap)
			if id := domain.StringValue(plan.sets[setIndex].items[itemIndex]["id"]); id != "" {
				plan.selectedID[id] = true
			}
		}
	}
	if selected[domain.BackupScopeProjects] {
		plan.outputError = reconcileRestoredOutputs(current, incoming, uploadID)
		for _, item := range incoming.ApplicationTemplates {
			if err := validateApplicationTemplate(item); err != nil {
				plan.outputError = err
				break
			}
			id := stringValue(item["id"])
			existing := findObject(current.ApplicationTemplates, id)
			if existing != nil {
				if templateContentsEqual(item, existing) {
					continue
				}
				plan.conflicts = append(plan.conflicts, domain.RestoreConflict{ID: stableRestoreID("conflict", "application_template", id, id, id), Kind: "application_template", SourceID: id, DestinationID: id, NaturalKey: id, ReasonCodes: []string{"ID_CONFLICT"}, Summary: "已有同标识的应用模板"})
			}
			plan.templates = append(plan.templates, item)
		}
	}
	if selected[domain.BackupScopeFrpTunnels] {
		for _, tunnel := range incoming.FrpTunnels {
			if tunnel.ID != "" {
				plan.selectedID[tunnel.ID] = true
			}
		}
		plan.frpTunnels = planRestoreFrpTunnels(current, incoming.FrpTunnels, plan.idMap, uploadID, &plan.conflicts)
		for _, tunnel := range plan.frpTunnels {
			plan.selectedID[tunnel.ID] = true
		}
	}
	sort.Slice(plan.conflicts, func(i, j int) bool {
		if plan.conflicts[i].Kind != plan.conflicts[j].Kind {
			return plan.conflicts[i].Kind < plan.conflicts[j].Kind
		}
		return plan.conflicts[i].NaturalKey < plan.conflicts[j].NaturalKey
	})
	return plan
}

func restoreObjectSets(incoming *domain.State, selected map[domain.BackupScope]bool) []restoreObjectSet {
	sets := []restoreObjectSet{}
	add := func(enabled bool, kind string, items []domain.Object, target func(*domain.State) *[]domain.Object, key func(domain.Object) string) {
		if enabled {
			sets = append(sets, restoreObjectSet{kind: kind, items: items, target: target, naturalKey: key})
		}
	}
	add(selected[domain.BackupScopeProjects], "project", incoming.Projects, func(state *domain.State) *[]domain.Object { return &state.Projects }, keyBy("slug", "name"))
	add(selected[domain.BackupScopeServers], "server", incoming.Servers, func(state *domain.State) *[]domain.Object { return &state.Servers }, keyBy("publicHost"))
	add(selected[domain.BackupScopeDomainsCertificates], "domain", incoming.Domains, func(state *domain.State) *[]domain.Object { return &state.Domains }, keyBy("name"))
	add(selected[domain.BackupScopeDomainsCertificates], "ingress", incoming.Ingresses, func(state *domain.State) *[]domain.Object { return &state.Ingresses }, keyBy("name"))
	add(selected[domain.BackupScopeDomainsCertificates], "resolver", incoming.Resolvers, func(state *domain.State) *[]domain.Object { return &state.Resolvers }, keyBy("name"))
	add(selected[domain.BackupScopeDomainsCertificates], "certificate", incoming.Certificates, func(state *domain.State) *[]domain.Object { return &state.Certificates }, keyBy("name"))
	add(selected[domain.BackupScopeSecretAssets], "secret_asset", incoming.SecretAssets, func(state *domain.State) *[]domain.Object { return &state.SecretAssets }, compositeKey("assetType", "name"))
	add(selected[domain.BackupScopeProjects], "application", incoming.Applications, func(state *domain.State) *[]domain.Object { return &state.Applications }, scopedNaturalKey("projectId", "slug", "name"))
	add(selected[domain.BackupScopeProjects], "application_deployment_config", incoming.ApplicationDeploymentConfigs, func(state *domain.State) *[]domain.Object { return &state.ApplicationDeploymentConfigs }, keyBy("applicationId"))
	add(selected[domain.BackupScopeProjects], "route", incoming.Routes, func(state *domain.State) *[]domain.Object { return &state.Routes }, restoreRouteKey)
	add(selected[domain.BackupScopeServers], "server_role", incoming.ServerRoles, func(state *domain.State) *[]domain.Object { return &state.ServerRoles }, compositeKey("serverId", "role"))
	return sets
}

func restoreRouteKey(route domain.Object) string {
	domains := uniqueStrings(objectStringSlice(route["domainIds"]))
	paths := []string{}
	for _, rule := range objectSlice(route["rules"]) {
		paths = append(paths, stringValue(rule["path"]))
	}
	sort.Strings(domains)
	sort.Strings(paths)
	return strings.Join([]string{stringValue(route["applicationId"]), strings.Join(domains, ","), strings.Join(paths, ",")}, "\x00")
}

func applyRestoreObjectPlan(state *domain.State, plan restoreObjectPlan, policy domain.RestoreConflictPolicy) error {
	if plan.outputError != nil {
		return plan.outputError
	}
	if policy == domain.RestoreConflictAbortOnConflict && len(plan.conflicts) > 0 {
		return errors.New("RESTORE_CONFLICTS_PRESENT")
	}
	if policy != domain.RestoreConflictAbortOnConflict && policy != domain.RestoreConflictOverwriteSelected {
		return errors.New("RESTORE_CONFLICT_POLICY_INVALID")
	}
	now := domain.Now()
	for _, item := range plan.templates {
		id := stringValue(item["id"])
		state.ApplicationTemplates = slices.DeleteFunc(state.ApplicationTemplates, func(current domain.Object) bool { return current["id"] == id })
		state.ApplicationTemplates = append(state.ApplicationTemplates, item)
	}
	for _, set := range plan.sets {
		target := set.target(state)
		for _, incoming := range set.items {
			if set.kind == "project" && domain.StringValue(incoming["id"]) == domain.BuiltinProjectID {
				domain.EnsureBuiltinProject(state)
				continue
			}
			item := domain.CloneObject(incoming)
			key := set.naturalKey(item)
			index := findObjectIndexByNaturalKey(*target, set.naturalKey, key)
			if index >= 0 {
				if policy != domain.RestoreConflictOverwriteSelected {
					return errors.New("RESTORE_CONFLICTS_PRESENT")
				}
				existing := (*target)[index]
				if set.kind == "domain" && redirectOccupiesHost(existing) {
					// 覆盖草稿也必须保留本机真实的远端快照，以便后续撤销。
					runtime := cloneObject(objectMapFromAny(existing["redirectRuntime"]))
					runtime["status"] = "pending"
					item["redirectRuntime"] = runtime
					if len(objectMapFromAny(item["redirect"])) == 0 {
						item["redirect"] = cloneObject(objectMapFromAny(existing["redirect"]))
					}
				}
				item["id"] = existing["id"]
				if createdAt := domain.StringValue(existing["createdAt"]); createdAt != "" {
					item["createdAt"] = createdAt
				}
				item["updatedAt"] = now
				(*target)[index] = item
				continue
			}
			if domain.StringValue(item["createdAt"]) == "" {
				item["createdAt"] = now
			}
			item["updatedAt"] = now
			*target = append(*target, item)
		}
	}
	if plan.selected[domain.BackupScopeDomainsCertificates] {
		for _, asset := range state.Domains {
			if draft := objectMapFromAny(asset["redirect"]); len(draft) > 0 {
				if err := validateRedirectDefinition(*state, stringValue(asset["id"]), draft); err != nil {
					return err
				}
			}
		}
	}
	return applyRestoreFrpTunnels(state, plan)
}

func applyRestoreFrpTunnels(state *domain.State, plan restoreObjectPlan) error {
	if !plan.selected[domain.BackupScopeFrpTunnels] {
		return nil
	}
	// FRP packages contain definitions only. Runs, tokens, process directories and
	// remote identities are intentionally never reconstructed.
	for _, incoming := range plan.frpTunnels {
		index := -1
		for candidate := range state.FrpTunnels {
			if strings.EqualFold(strings.TrimSpace(state.FrpTunnels[candidate].Name), strings.TrimSpace(incoming.Name)) && state.FrpTunnels[candidate].ServerID == incoming.ServerID {
				index = candidate
				break
			}
		}
		if index >= 0 {
			state.FrpTunnels[index] = incoming
			continue
		}
		state.FrpTunnels = append(state.FrpTunnels, incoming)
	}
	return nil
}

func planRestoreFrpTunnels(current domain.State, tunnels []domain.FrpTunnel, idMap map[string]string, uploadID string, conflicts *[]domain.RestoreConflict) []domain.FrpTunnel {
	result := make([]domain.FrpTunnel, 0, len(tunnels))
	occupied := stateObjectIDs(current)
	for _, source := range tunnels {
		incoming := source
		if mapped := idMap[incoming.ServerID]; mapped != "" {
			incoming.ServerID = mapped
		}
		destinationIndex := -1
		for index := range current.FrpTunnels {
			if strings.EqualFold(strings.TrimSpace(current.FrpTunnels[index].Name), strings.TrimSpace(incoming.Name)) && current.FrpTunnels[index].ServerID == incoming.ServerID {
				destinationIndex = index
				break
			}
		}
		if destinationIndex >= 0 {
			destination := current.FrpTunnels[destinationIndex]
			*conflicts = append(*conflicts, domain.RestoreConflict{
				ID:   stableRestoreID("conflict", "frp_tunnel", source.ID, destination.ID, incoming.ServerID+"\x00"+incoming.Name),
				Kind: "frp_tunnel", SourceID: source.ID, DestinationID: destination.ID,
				NaturalKey:  incoming.ServerID + "\x00" + strings.ToLower(strings.TrimSpace(incoming.Name)),
				ReasonCodes: []string{"NATURAL_KEY_CONFLICT"}, Summary: "目标中已存在相同服务器和名称的内网穿透定义",
			})
			idMap[source.ID] = destination.ID
			incoming.ID = destination.ID
			incoming.CreatedAt = destination.CreatedAt
			incoming.UpdatedAt = domain.Now()
			result = append(result, incoming)
			continue
		}
		if incoming.ID == "" || occupied[incoming.ID] {
			incoming.ID = uniqueRestoredObjectID("frp_tunnel", source.ID, uploadID, occupied)
			if source.ID != "" {
				idMap[source.ID] = incoming.ID
			}
		}
		occupied[incoming.ID] = true
		incoming.UpdatedAt = domain.Now()
		if incoming.CreatedAt == "" {
			incoming.CreatedAt = incoming.UpdatedAt
		}
		result = append(result, incoming)
	}
	return result
}

func appendRestoreHistory(state *domain.State, history *domain.BackupLogsAndAuditData, idMap map[string]string, secretMap map[string]string, selectedIDs map[string]bool) {
	if history == nil {
		return
	}
	for _, source := range history.RuntimeLogs {
		if !selectedIDs[source.TargetID] {
			continue
		}
		item := source
		item.ID = uniqueLogOrAuditID("log", item.ID, *state)
		item.OperationRunID = ""
		item.Normalize()
		item.TargetID = mappedRestoreString(item.TargetID, idMap, secretMap)
		state.RuntimeLogs = append(state.RuntimeLogs, item)
	}
	for _, source := range history.OperationLogs {
		if !selectedIDs[source.TargetID] {
			continue
		}
		item := source
		item.ID = uniqueLogOrAuditID("operation_log", item.ID, *state)
		item.OperationRunID = ""
		item.Normalize()
		item.TargetID = mappedRestoreString(item.TargetID, idMap, secretMap)
		state.OperationLogs = append(state.OperationLogs, item)
	}
}

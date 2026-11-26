package application

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func buildRestoreSecretRefMap(current domain.State, incoming domain.State, idMap map[string]string, uploadID string) map[string]string {
	result := map[string]string{}
	currentRefs := currentSecretRefs(current)
	bindDestinationRefs := func(sources []domain.Object, destinations []domain.Object, refKeys ...string) {
		for _, source := range sources {
			sourceID := domain.StringValue(source["id"])
			destinationID := idMap[sourceID]
			if destinationID == "" || destinationID == sourceID {
				continue
			}
			destination := findObjectByID(destinations, destinationID)
			if destination == nil {
				continue
			}
			for _, key := range refKeys {
				sourceRef := domain.StringValue(source[key])
				destinationRef := domain.StringValue(destination[key])
				if sourceRef != "" && destinationRef != "" {
					result[sourceRef] = destinationRef
				}
			}
		}
	}
	bindDestinationRefs(incoming.SecretAssets, current.SecretAssets, "secretRef")
	bindDestinationRefs(incoming.Certificates, current.Certificates, "crtSecretRef", "keySecretRef")

	incomingPayload := domain.BuildBackupPayload(incoming, domain.BackupSchemaVersion, domain.BackupScopes(), false)
	for _, ref := range domain.BackupPayloadSecretRefs(incomingPayload) {
		if result[ref] != "" {
			continue
		}
		if currentRefs[ref] {
			result[ref] = "secret_restore_" + stableRestoreHash(uploadID, ref)[:24]
		}
	}
	return result
}

func buildRestoreUploadAssetMap(current domain.State, incoming domain.State, idMap map[string]string, uploadID string) map[string]string {
	result := map[string]string{}
	occupied := map[string]bool{}
	for _, group := range [][]domain.Object{current.Applications, current.ApplicationDeploymentConfigs} {
		for _, item := range group {
			if assetID := uploadAssetID(item); assetID != "" {
				occupied[assetID] = true
			}
		}
	}
	for _, pair := range [][2][]domain.Object{
		{incoming.Applications, current.Applications},
		{incoming.ApplicationDeploymentConfigs, current.ApplicationDeploymentConfigs},
	} {
		for _, source := range pair[0] {
			sourceAssetID := uploadAssetID(source)
			if sourceAssetID == "" {
				continue
			}
			destinationID := idMap[domain.StringValue(source["id"])]
			if destination := findObjectByID(pair[1], destinationID); destination != nil {
				if destinationAssetID := uploadAssetID(destination); destinationAssetID != "" {
					result[sourceAssetID] = destinationAssetID
					continue
				}
			}
			if occupied[sourceAssetID] {
				result[sourceAssetID] = uniqueRestoredObjectID("upload", sourceAssetID, uploadID, occupied)
				occupied[result[sourceAssetID]] = true
				continue
			}
			occupied[sourceAssetID] = true
		}
	}
	return result
}

func buildFilesystemUploadAssetMap(current domain.State, payload domain.BackupPayload, idMap map[string]string, uploadID string, stateDir string) map[string]string {
	result := map[string]string{}
	if payload.Projects == nil || strings.TrimSpace(stateDir) == "" {
		return result
	}
	occupied := map[string]bool{}
	for _, group := range [][]domain.Object{current.Applications, current.ApplicationDeploymentConfigs} {
		for _, item := range group {
			if assetID := uploadAssetID(item); assetID != "" {
				occupied[assetID] = true
			}
		}
	}
	for _, metadata := range payload.Projects.UploadFiles {
		assetID := strings.TrimSpace(metadata.AssetID)
		if !validBackupAssetID(assetID) {
			continue
		}
		if mapped := idMap[assetID]; mapped != "" {
			occupied[mapped] = true
			continue
		}
		if _, err := os.Stat(filepath.Join(stateDir, "uploads", assetID)); err == nil || occupied[assetID] {
			mapped := uniqueRestoredObjectID("upload", assetID, uploadID, occupied)
			result[assetID] = mapped
			occupied[mapped] = true
			continue
		}
		occupied[assetID] = true
	}
	return result
}

func uploadAssetID(application domain.Object) string {
	source, ok := application["sourceConfig"].(map[string]any)
	if !ok {
		if object, objectOK := application["sourceConfig"].(domain.Object); objectOK {
			source = map[string]any(object)
		}
	}
	if domain.StringValue(source["provider"]) != "upload" {
		return ""
	}
	return domain.StringValue(source["artifactAssetId"])
}

func currentSecretRefs(state domain.State) map[string]bool {
	refs := map[string]bool{}
	payload := domain.BuildBackupPayload(state, domain.BackupSchemaVersion, domain.BackupScopes(), false)
	for _, ref := range domain.BackupPayloadSecretRefs(payload) {
		refs[ref] = true
	}
	return refs
}

func rewriteRestoreValue(value any, idMap map[string]string, secretMap map[string]string) {
	switch typed := value.(type) {
	case domain.Object:
		rewriteRestoreValue(map[string]any(typed), idMap, secretMap)
	case map[string]any:
		for key, nested := range typed {
			typed[key] = rewrittenRestoreField(key, nested, idMap, secretMap)
		}
	case []domain.Object:
		for _, nested := range typed {
			rewriteRestoreValue(nested, idMap, secretMap)
		}
	case []any:
		for index := range typed {
			typed[index] = rewrittenRestoreField("", typed[index], idMap, secretMap)
		}
	}
}

func rewrittenRestoreField(key string, value any, idMap map[string]string, secretMap map[string]string) any {
	if key == "outputVariables" || key == "environmentVariables" {
		rows := domain.CloneObjectSlice(objectSlice(value))
		for _, row := range rows {
			for field, nested := range row {
				// 输出和输入行的 ID 属于应用变量命名空间，不随顶层资源重写。
				if field != "id" && field != "outputId" {
					row[field] = rewrittenRestoreField(field, nested, idMap, secretMap)
				}
			}
		}
		return rows
	}
	switch typed := value.(type) {
	case string:
		if isRestoreSecretReferenceKey(key) {
			return mappedRestoreString(typed, nil, secretMap)
		}
		if isRestoreIDReferenceKey(key) {
			return mappedRestoreString(typed, idMap, nil)
		}
		return typed
	case domain.Object:
		result := domain.CloneObject(typed)
		rewriteRestoreValue(result, idMap, secretMap)
		return result
	case map[string]any:
		result := map[string]any(domain.CloneObject(typed))
		rewriteRestoreValue(result, idMap, secretMap)
		return result
	case []domain.Object:
		result := domain.CloneObjectSlice(typed)
		rewriteRestoreValue(result, idMap, secretMap)
		return result
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = rewrittenRestoreField(key, item, idMap, secretMap)
		}
		return result
	case []string:
		result := append([]string(nil), typed...)
		if isRestoreSecretReferenceKey(key) || isRestoreIDReferenceKey(key) {
			for index := range result {
				result[index] = rewrittenRestoreField(key, result[index], idMap, secretMap).(string)
			}
		}
		return result
	default:
		return value
	}
}

func isRestoreIDReferenceKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "id" || key == "ID" || key == "targetServers" {
		return true
	}
	return strings.HasSuffix(key, "Id") || strings.HasSuffix(key, "Ids") ||
		strings.HasSuffix(key, "ID") || strings.HasSuffix(key, "IDs") ||
		strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_ids")
}

func isRestoreSecretReferenceKey(key string) bool {
	key = strings.TrimSpace(key)
	return key == "materialRef" || key == "materialRevisionRef" || key == "secretRef" || strings.HasSuffix(key, "SecretRef") || strings.HasSuffix(key, "SecretRefs") ||
		key == "secret_ref" || strings.HasSuffix(key, "_secret_ref") || strings.HasSuffix(key, "_secret_refs")
}

func mappedRestoreString(value string, idMap map[string]string, secretMap map[string]string) string {
	if mapped := idMap[value]; mapped != "" {
		return mapped
	}
	if mapped := secretMap[value]; mapped != "" {
		return mapped
	}
	return value
}

func backupScopeSet(scopes []domain.BackupScope) map[domain.BackupScope]bool {
	result := make(map[domain.BackupScope]bool, len(scopes))
	for _, scope := range scopes {
		result[scope] = true
	}
	return result
}

func keyBy(keys ...string) func(domain.Object) string {
	return func(item domain.Object) string {
		for _, key := range keys {
			if value := normalizeNaturalKey(domain.StringValue(item[key])); value != "" {
				return value
			}
		}
		return ""
	}
}

func compositeKey(keys ...string) func(domain.Object) string {
	return func(item domain.Object) string {
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			value := normalizeNaturalKey(domain.StringValue(item[key]))
			if value != "" {
				parts = append(parts, key+"="+value)
			}
		}
		return strings.Join(parts, "\x00")
	}
}

func scopedNaturalKey(scopeKey string, identityKeys ...string) func(domain.Object) string {
	return func(item domain.Object) string {
		scope := normalizeNaturalKey(domain.StringValue(item[scopeKey]))
		identity := ""
		for _, key := range identityKeys {
			identity = normalizeNaturalKey(domain.StringValue(item[key]))
			if identity != "" {
				break
			}
		}
		if scope == "" || identity == "" {
			return ""
		}
		return scopeKey + "=" + scope + "\x00identity=" + identity
	}
}

func normalizeNaturalKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func findObjectByNaturalKey(items []domain.Object, key func(domain.Object) string, value string) domain.Object {
	index := findObjectIndexByNaturalKey(items, key, value)
	if index < 0 {
		return nil
	}
	return items[index]
}

func findObjectIndexByNaturalKey(items []domain.Object, key func(domain.Object) string, value string) int {
	if value == "" {
		return -1
	}
	for index, item := range items {
		if key(item) == value {
			return index
		}
	}
	return -1
}

func findObjectByID(items []domain.Object, id string) domain.Object {
	for _, item := range items {
		if domain.StringValue(item["id"]) == id {
			return item
		}
	}
	return nil
}

func stateObjectIDs(state domain.State) map[string]bool {
	result := map[string]bool{}
	groups := [][]domain.Object{
		state.Servers, state.ServerRoles, state.SecretAssets, state.Domains, state.Certificates,
		state.Resolvers, state.Ingresses, state.MonitoringAgents, state.Projects, state.Applications,
		state.ApplicationInstances, state.ApplicationDeploymentConfigs, state.Routes,
		state.ReleasePlans, state.BuildLogs, state.RuntimeLogStreams, state.JenkinsReleaseRuns,
		state.HealthSnapshots, state.MetricSamples, state.MetricAggregates, state.SwarmSnapshots,
		state.BackupRestoreJobs,
	}
	for _, group := range groups {
		for _, item := range group {
			if id := domain.StringValue(item["id"]); id != "" {
				result[id] = true
			}
		}
	}
	for _, tunnel := range state.FrpTunnels {
		result[tunnel.ID] = true
	}
	for _, task := range state.Tasks {
		result[task.ID] = true
	}
	return result
}

func uniqueRestoredObjectID(kind string, sourceID string, uploadID string, occupied map[string]bool) string {
	prefix := strings.TrimSpace(kind)
	if index := strings.Index(sourceID, "_"); index > 0 {
		prefix = sourceID[:index]
	}
	if prefix == "" {
		prefix = "restored"
	}
	base := prefix + "_restored_" + stableRestoreHash(kind, sourceID, uploadID)[:16]
	for candidate, suffix := base, 1; ; suffix++ {
		if !occupied[candidate] {
			return candidate
		}
		candidate = fmt.Sprintf("%s_%d", base, suffix)
	}
}

func uniqueHistoryID(prefix string, sourceID string, occupied map[string]bool) string {
	base := prefix + "_restored_" + stableRestoreHash(prefix, sourceID)[:16]
	for candidate, suffix := base, 1; ; suffix++ {
		if !occupied[candidate] {
			return candidate
		}
		candidate = fmt.Sprintf("%s_%d", base, suffix)
	}
}

func uniqueLogOrAuditID(prefix string, sourceID string, state domain.State) string {
	occupied := map[string]bool{}
	for _, item := range state.RuntimeLogs {
		occupied[item.ID] = true
	}
	for _, item := range state.OperationLogs {
		occupied[item.ID] = true
	}
	if sourceID != "" && !occupied[sourceID] {
		return sourceID
	}
	return uniqueHistoryID(prefix, sourceID, occupied)
}

func stableRestoreID(prefix string, values ...string) string {
	return prefix + "_" + stableRestoreHash(values...)[:24]
}

func stableRestoreHash(values ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(hash[:])
}

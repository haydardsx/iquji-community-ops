package domain

import (
	"sort"
	"strings"
)

func orderedBackupScopeSet(scopes map[BackupScope]bool) []BackupScope {
	result := make([]BackupScope, 0, len(scopes))
	for _, scope := range backupScopeOrder {
		if scopes[scope] {
			result = append(result, scope)
		}
	}
	return result
}

func backupScopeIndex(scope BackupScope) int {
	for index, candidate := range backupScopeOrder {
		if candidate == scope {
			return index
		}
	}
	return len(backupScopeOrder)
}

var serverReferenceKeys = map[string]bool{
	"serverid": true, "serverids": true, "buildserverid": true, "gatewayserverid": true, "eligibleserverids": true,
	"targetserverid": true, "targetserverids": true, "targetservers": true, "traefikserverids": true,
}

func appendObjectGroups(groups ...[]Object) []Object {
	var result []Object
	for _, group := range groups {
		result = append(result, group...)
	}
	return result
}

func containsReferenceKeys(objects []Object, keys map[string]bool) bool {
	for _, object := range objects {
		if anyContainsReferenceKey(object, keys) {
			return true
		}
	}
	return false
}

func anyContainsReferenceKey(value any, keys map[string]bool) bool {
	switch typed := value.(type) {
	case Object:
		return anyContainsReferenceKey(map[string]any(typed), keys)
	case map[string]any:
		for key, item := range typed {
			if keys[strings.ToLower(key)] && referenceValuePresent(item) {
				return true
			}
			if anyContainsReferenceKey(item, keys) {
				return true
			}
		}
	case []Object:
		for _, item := range typed {
			if anyContainsReferenceKey(item, keys) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if anyContainsReferenceKey(item, keys) {
				return true
			}
		}
	}
	return false
}

func referenceValuePresent(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []string:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return value != nil
	}
}

func routesContainDomainReferences(routes []Object) bool {
	for _, route := range routes {
		if referenceValuePresent(route["domainIds"]) || referenceValuePresent(route["domains"]) {
			return true
		}
	}
	return false
}

func containsSecretReference(objects []Object) bool {
	for _, object := range objects {
		refs := map[string]bool{}
		collectSecretRefs(object, refs)
		if len(refs) > 0 || anyContainsReferenceKey(object, map[string]bool{"secretassetid": true, "secretassetids": true}) {
			return true
		}
	}
	return false
}

func collectSecretRefs(value any, refs map[string]bool) {
	switch typed := value.(type) {
	case Object:
		collectSecretRefs(map[string]any(typed), refs)
	case map[string]any:
		for key, item := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			if normalized == "materialrevisionref" || normalized == "materialref" || normalized == "secretref" || normalized == "secretrefs" ||
				strings.HasSuffix(normalized, "secretref") || strings.HasSuffix(normalized, "secretrefs") {
				switch ref := item.(type) {
				case string:
					if strings.TrimSpace(ref) != "" {
						refs[ref] = true
					}
				case []string:
					for _, candidate := range ref {
						if strings.TrimSpace(candidate) != "" {
							refs[candidate] = true
						}
					}
				case []any:
					for _, candidate := range ref {
						if value, ok := candidate.(string); ok && strings.TrimSpace(value) != "" {
							refs[value] = true
						}
					}
				}
			}
			collectSecretRefs(item, refs)
		}
	case []Object:
		for _, item := range typed {
			collectSecretRefs(item, refs)
		}
	case []any:
		for _, item := range typed {
			collectSecretRefs(item, refs)
		}
	}
}

func containsInlineSecret(objects []Object) bool {
	for _, object := range objects {
		if anyContainsInlineSecret(object) {
			return true
		}
	}
	return false
}

func anyContainsInlineSecret(value any) bool {
	switch typed := value.(type) {
	case Object:
		return anyContainsInlineSecret(map[string]any(typed))
	case map[string]any:
		for key, item := range typed {
			if isInlineSecretKey(key) && referenceValuePresent(item) {
				return true
			}
			if anyContainsInlineSecret(item) {
				return true
			}
		}
	case []Object:
		for _, item := range typed {
			if anyContainsInlineSecret(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if anyContainsInlineSecret(item) {
				return true
			}
		}
	}
	return false
}

func isInlineSecretKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	return normalized == "certificatepem" || normalized == "privatekeypem" || normalized == "privatekey" ||
		normalized == "secretvalue" || normalized == "secretmaterial" || normalized == "artifactcontentbase64" ||
		normalized == "password" || normalized == "apitoken" || normalized == "frptoken"
}

func sanitizeObjectSlice(items []Object, replacements map[string]string) []Object {
	result := make([]Object, 0, len(items))
	for _, item := range items {
		cloned, ok := sanitizeBackupValue(item).(map[string]any)
		if !ok {
			continue
		}
		for key, replacement := range replacements {
			if replacement == "" {
				delete(cloned, key)
			} else {
				cloned[key] = replacement
			}
		}
		result = append(result, Object(cloned))
	}
	sortObjects(result)
	return result
}

func sanitizeBackupValue(value any) any {
	switch typed := value.(type) {
	case Object:
		return sanitizeBackupValue(map[string]any(typed))
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if isInlineSecretKey(key) || isLegacyUserReferenceKey(key) {
				continue
			}
			if key == "dockerRegistryMirrorUrl" || key == "registryMirrorUrl" {
				if _, ok := DockerRegistryMirrorURL(item); !ok {
					continue
				}
			}
			result[key] = sanitizeBackupValue(item)
		}
		return result
	case []Object:
		result := make([]Object, 0, len(typed))
		for _, item := range typed {
			if cloned, ok := sanitizeBackupValue(item).(map[string]any); ok {
				result = append(result, Object(cloned))
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = sanitizeBackupValue(item)
		}
		return result
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}

func normalizeBackupRoutes(routes []Object) []Object {
	result := sanitizeObjectSlice(routes, map[string]string{"status": "pending", "lastTaskId": ""})
	for _, route := range result {
		domainIDs := StringSliceFromAny(route["domainIds"])
		if len(domainIDs) == 0 {
			for _, item := range ObjectSliceFromAny(route["domains"]) {
				if id := StringValue(item["id"]); id != "" {
					domainIDs = append(domainIDs, id)
				}
			}
		}
		domainIDs = sortedUniqueStrings(domainIDs)
		route["domainIds"] = domainIDs
		delete(route, "domains")
		delete(route, "accessibleUrls")
		delete(route, "traefikLocator")
		delete(route, "lastApplied")
		delete(route, "pendingRemote")
	}
	return result
}

func backupUploadFiles(groups ...[]Object) []BackupFileMetadata {
	result := []BackupFileMetadata{}
	seen := map[string]bool{}
	for _, group := range groups {
		for _, item := range group {
			source := ObjectFromAny(item["sourceConfig"])
			if StringValue(source["provider"]) != "upload" {
				continue
			}
			assetID := StringValue(source["artifactAssetId"])
			if assetID == "" || seen[assetID] {
				continue
			}
			seen[assetID] = true
			fileName := StringValue(source["artifactFileName"])
			if fileName == "" {
				fileName = assetID
			}
			result = append(result, BackupFileMetadata{
				ArchivePath: "files/uploads/" + assetID,
				AssetID:     assetID,
				FileName:    fileName,
				SHA256:      StringValue(source["artifactSha256"]),
				SizeBytes:   int64(NumberValue(source["artifactSizeBytes"])),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ArchivePath < result[j].ArchivePath })
	return result
}

func collectObjectIDs(target map[string]bool, groups ...[]Object) {
	for _, group := range groups {
		for _, item := range group {
			if id := StringValue(item["id"]); id != "" {
				target[id] = true
			}
		}
	}
}

func buildRelatedBackupHistory(state State, selectedIDs map[string]bool) *BackupLogsAndAuditData {
	runtimeLogs := make([]RuntimeLog, 0)
	for _, item := range state.RuntimeLogs {
		if selectedIDs[item.TargetID] {
			runtimeLogs = append(runtimeLogs, item)
		}
	}
	sort.Slice(runtimeLogs, func(i, j int) bool { return runtimeLogs[i].ID < runtimeLogs[j].ID })
	operationLogs := make([]OperationLog, 0)
	for _, item := range state.OperationLogs {
		if selectedIDs[item.TargetID] {
			operationLogs = append(operationLogs, item)
		}
	}
	sort.Slice(operationLogs, func(i, j int) bool { return operationLogs[i].ID < operationLogs[j].ID })

	return &BackupLogsAndAuditData{
		RuntimeLogs:   runtimeLogs,
		OperationLogs: operationLogs,
	}
}

func sortObjects(items []Object) {
	sort.SliceStable(items, func(i, j int) bool {
		left := StringValue(items[i]["id"])
		right := StringValue(items[j]["id"])
		if left == right {
			return StringValue(items[i]["name"]) < StringValue(items[j]["name"])
		}
		return left < right
	})
}

func sortedUniqueStrings(items []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}

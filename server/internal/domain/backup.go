package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	BackupArchiveFormat  = "iquops-backup"
	BackupArchiveVersion = 1
)

var (
	ErrBackupScopeRequired  = errors.New("BACKUP_SCOPE_REQUIRED")
	ErrBackupScopeDuplicate = errors.New("BACKUP_SCOPE_DUPLICATE")
	ErrBackupScopeUnknown   = errors.New("BACKUP_SCOPE_UNKNOWN")
)

type BackupScope string

const (
	BackupScopeProjects            BackupScope = "projects"
	BackupScopeServers             BackupScope = "servers"
	BackupScopeDomainsCertificates BackupScope = "domains_certificates"
	BackupScopeSecretAssets        BackupScope = "secret_assets"
	BackupScopeFrpTunnels          BackupScope = "frp_tunnels"
)

var backupScopeOrder = []BackupScope{
	BackupScopeProjects,
	BackupScopeServers,
	BackupScopeDomainsCertificates,
	BackupScopeSecretAssets,
	BackupScopeFrpTunnels,
}

type BackupOptions struct {
	RequestedScopes      []BackupScope `json:"requestedScopes"`
	IncludeSensitiveData bool          `json:"includeSensitiveData"`
	IncludeLogsAndAudit  bool          `json:"includeLogsAndAudit"`
}

type RestoreConflictPolicy string

const (
	RestoreConflictAbortOnConflict   RestoreConflictPolicy = "abort_on_conflict"
	RestoreConflictOverwriteSelected RestoreConflictPolicy = "overwrite_selected"
)

type RestoreOptions struct {
	RequestedScopes     []BackupScope         `json:"requestedScopes"`
	IncludeLogsAndAudit bool                  `json:"includeLogsAndAudit"`
	ConflictPolicy      RestoreConflictPolicy `json:"conflictPolicy"`
}

type BackupDependency struct {
	Scope           BackupScope `json:"scope"`
	RequiredByScope BackupScope `json:"requiredByScope"`
	Resource        ResourceRef `json:"resource"`
	Reason          string      `json:"reason"`
}

type BackupFileMetadata struct {
	ArchivePath string `json:"archivePath"`
	AssetID     string `json:"assetId,omitempty"`
	FileName    string `json:"fileName"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"sizeBytes"`
}

type BackupManifest struct {
	ApplicationVersion    string             `json:"applicationVersion"`
	SchemaVersion         string             `json:"schemaVersion"`
	RequestedScopes       []BackupScope      `json:"requestedScopes"`
	EffectiveScopes       []BackupScope      `json:"effectiveScopes"`
	DependencyReasons     []BackupDependency `json:"dependencyReasons"`
	RecordCounts          map[string]int     `json:"recordCounts"`
	IncludesSensitiveData bool               `json:"includesSensitiveData"`
	IncludesLogsAndAudit  bool               `json:"includesLogsAndAudit"`
	PayloadSHA256         string             `json:"payloadSha256"`
	CreatedAt             string             `json:"createdAt"`
}

type BackupProjectsData struct {
	ApplicationTemplates         []Object             `json:"applicationTemplates,omitzero"`
	Projects                     []Object             `json:"projects"`
	Applications                 []Object             `json:"applications"`
	ApplicationDeploymentConfigs []Object             `json:"applicationDeploymentConfigs"`
	Routes                       []Object             `json:"routes"`
	UploadFiles                  []BackupFileMetadata `json:"uploadFiles"`
}

type BackupServersData struct {
	Servers     []Object `json:"servers"`
	ServerRoles []Object `json:"serverRoles"`
}

type BackupDomainsCertificatesData struct {
	Domains      []Object `json:"domains"`
	Certificates []Object `json:"certificates"`
	Resolvers    []Object `json:"resolvers"`
	Ingresses    []Object `json:"ingresses"`
}

type BackupSecretAssetsData struct {
	Assets []Object `json:"assets"`
}

type BackupFrpTunnelsData struct {
	Tunnels []FrpTunnel `json:"tunnels"`
}

type BackupLogsAndAuditData struct {
	RuntimeLogs   []RuntimeLog   `json:"runtimeLogs"`
	OperationLogs []OperationLog `json:"operationLogs"`
}

type BackupSecretMaterial struct {
	SecretRef string `json:"secretRef"`
	Value     string `json:"value"`
}

type BackupPayload struct {
	SchemaVersion       string                         `json:"schemaVersion"`
	Scopes              []BackupScope                  `json:"scopes"`
	Projects            *BackupProjectsData            `json:"projects,omitempty"`
	Servers             *BackupServersData             `json:"servers,omitempty"`
	DomainsCertificates *BackupDomainsCertificatesData `json:"domainsCertificates,omitempty"`
	SecretAssets        *BackupSecretAssetsData        `json:"secretAssets,omitempty"`
	FrpTunnels          *BackupFrpTunnelsData          `json:"frpTunnels,omitempty"`
	LogsAndAudit        *BackupLogsAndAuditData        `json:"logsAndAudit,omitempty"`
	SecretMaterials     []BackupSecretMaterial         `json:"secretMaterials,omitempty"`
}

type BackupPackage struct {
	Manifest BackupManifest `json:"manifest"`
	Payload  BackupPayload  `json:"payload"`
}

func BackupScopes() []BackupScope {
	return append([]BackupScope(nil), backupScopeOrder...)
}

func NormalizeBackupScopes(scopes []BackupScope) ([]BackupScope, error) {
	if len(scopes) == 0 {
		return nil, ErrBackupScopeRequired
	}
	seen := make(map[BackupScope]bool, len(scopes))
	for _, scope := range scopes {
		if !isBackupScope(scope) {
			return nil, fmt.Errorf("%w:%s", ErrBackupScopeUnknown, scope)
		}
		if seen[scope] {
			return nil, fmt.Errorf("%w:%s", ErrBackupScopeDuplicate, scope)
		}
		seen[scope] = true
	}
	return orderedBackupScopeSet(seen), nil
}

func ResolveBackupScopeClosure(state State, requested []BackupScope) ([]BackupScope, []BackupDependency, error) {
	normalized, err := NormalizeBackupScopes(requested)
	if err != nil {
		return nil, nil, err
	}
	effective := make(map[BackupScope]bool, len(normalized))
	for _, scope := range normalized {
		effective[scope] = true
	}
	dependencies := map[string]BackupDependency{}
	add := func(requiredBy BackupScope, scope BackupScope, reason string) bool {
		if requiredBy == scope {
			return false
		}
		key := string(scope) + "\x00" + string(requiredBy) + "\x00" + reason
		dependencies[key] = BackupDependency{
			Scope:           scope,
			RequiredByScope: requiredBy,
			Resource:        ResourceRef{Kind: "backup_scope", ID: string(requiredBy), Name: string(requiredBy)},
			Reason:          reason,
		}
		if effective[scope] {
			return false
		}
		effective[scope] = true
		return true
	}

	for changed := true; changed; {
		changed = false
		if effective[BackupScopeProjects] {
			projectObjects := appendObjectGroups(state.Projects, state.Applications, state.ApplicationDeploymentConfigs, state.Routes)
			if containsReferenceKeys(projectObjects, serverReferenceKeys) {
				changed = add(BackupScopeProjects, BackupScopeServers, "PROJECT_SERVER_REFERENCE") || changed
			}
			if routesContainDomainReferences(state.Routes) {
				changed = add(BackupScopeProjects, BackupScopeDomainsCertificates, "PROJECT_ROUTE_DOMAIN_REFERENCE") || changed
			}
			if containsSecretReference(projectObjects) {
				changed = add(BackupScopeProjects, BackupScopeSecretAssets, "PROJECT_SECRET_REFERENCE") || changed
			}
		}
		if effective[BackupScopeServers] {
			if containsSecretReference(state.Servers) {
				changed = add(BackupScopeServers, BackupScopeSecretAssets, "SERVER_SECRET_BINDING") || changed
			}
		}
		if effective[BackupScopeDomainsCertificates] {
			domainObjects := appendObjectGroups(state.Domains, state.Certificates, state.Resolvers, state.Ingresses)
			if containsReferenceKeys(state.Ingresses, serverReferenceKeys) {
				changed = add(BackupScopeDomainsCertificates, BackupScopeServers, "INGRESS_SERVER_REFERENCE") || changed
			}
			for _, asset := range state.Domains {
				if StringValue(ObjectFromAny(asset["redirect"])["gatewayServerId"]) != "" {
					changed = add(BackupScopeDomainsCertificates, BackupScopeServers, "DOMAIN_REDIRECT_GATEWAY_REFERENCE") || changed
					break
				}
			}
			if containsSecretReference(domainObjects) || containsInlineSecret(domainObjects) {
				changed = add(BackupScopeDomainsCertificates, BackupScopeSecretAssets, "CERTIFICATE_SECRET_REFERENCE") || changed
			}
		}
		if effective[BackupScopeFrpTunnels] && len(state.FrpTunnels) > 0 {
			changed = add(BackupScopeFrpTunnels, BackupScopeServers, "FRP_TUNNEL_SERVER_REFERENCE") || changed
		}
	}

	resolved := orderedBackupScopeSet(effective)
	items := make([]BackupDependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		items = append(items, dependency)
	}
	sort.Slice(items, func(i, j int) bool {
		left := backupScopeIndex(items[i].Scope)
		right := backupScopeIndex(items[j].Scope)
		if left != right {
			return left < right
		}
		left = backupScopeIndex(items[i].RequiredByScope)
		right = backupScopeIndex(items[j].RequiredByScope)
		if left != right {
			return left < right
		}
		return items[i].Reason < items[j].Reason
	})
	return resolved, items, nil
}

func BuildBackupPayload(state State, schemaVersion string, scopes []BackupScope, includeLogsAndAudit bool) BackupPayload {
	state.Applications = CloneObjectSlice(state.Applications)
	state.ApplicationDeploymentConfigs = CloneObjectSlice(state.ApplicationDeploymentConfigs)
	state.Routes = CloneObjectSlice(state.Routes)
	state.Domains = CloneObjectSlice(state.Domains)
	MigrateDomainRedirects(&state)
	MigrateApplicationSources(&state)
	MigrateApplicationDeploymentLocations(&state)
	MigrateRouteServiceWeights(&state)
	scopeSet := make(map[BackupScope]bool, len(scopes))
	for _, scope := range scopes {
		scopeSet[scope] = true
	}
	payload := BackupPayload{
		SchemaVersion:   schemaVersion,
		Scopes:          append([]BackupScope(nil), scopes...),
		SecretMaterials: []BackupSecretMaterial{},
	}
	selectedIDs := map[string]bool{}

	if scopeSet[BackupScopeProjects] {
		projects := sanitizeObjectSlice(state.Projects, nil)
		applications := sanitizeObjectSlice(state.Applications, map[string]string{"status": "", "runtimeSnapshot": "", "deploymentLocation": "", "appliedInputRevisions": "", "appliedInputSignatures": "", "appliedRuntimeKeys": "", "publishedBuildOutputs": ""})
		configs := sanitizeObjectSlice(state.ApplicationDeploymentConfigs, nil)
		routes := normalizeBackupRoutes(state.Routes)
		files := backupUploadFiles(applications, configs)
		payload.Projects = &BackupProjectsData{
			ApplicationTemplates:         CloneObjectSliceWithoutUserReferences(state.ApplicationTemplates),
			Projects:                     projects,
			Applications:                 applications,
			ApplicationDeploymentConfigs: configs,
			Routes:                       routes,
			UploadFiles:                  files,
		}
		collectObjectIDs(selectedIDs, projects, applications, configs, routes)
	}
	if scopeSet[BackupScopeServers] {
		servers := sanitizeObjectSlice(state.Servers, map[string]string{
			"status": "", "health": "", "connectionState": "", "lastHealthSnapshotId": "",
		})
		roles := sanitizeObjectSlice(state.ServerRoles, nil)
		NormalizeServerConnections(servers)
		payload.Servers = &BackupServersData{Servers: servers, ServerRoles: roles}
		collectObjectIDs(selectedIDs, servers, roles)
	}
	if scopeSet[BackupScopeDomainsCertificates] {
		domains := sanitizeObjectSlice(state.Domains, map[string]string{"redirectRuntime": ""})
		certificates := sanitizeObjectSlice(state.Certificates, nil)
		resolvers := sanitizeObjectSlice(state.Resolvers, nil)
		ingresses := sanitizeObjectSlice(state.Ingresses, nil)
		payload.DomainsCertificates = &BackupDomainsCertificatesData{
			Domains: domains, Certificates: certificates, Resolvers: resolvers, Ingresses: ingresses,
		}
		collectObjectIDs(selectedIDs, domains, certificates, resolvers, ingresses)
	}
	if scopeSet[BackupScopeSecretAssets] {
		confirmedAssets := []Object{}
		for _, asset := range state.SecretAssets {
			if StringValue(asset["pendingCreationOperationId"]) == "" {
				confirmedAssets = append(confirmedAssets, asset)
			}
		}
		assets := sanitizeObjectSlice(confirmedAssets, map[string]string{
			"pendingCreationOperationId": "", "appliedOperationRunId": "",
		})
		payload.SecretAssets = &BackupSecretAssetsData{Assets: assets}
		collectObjectIDs(selectedIDs, assets)
	}
	if scopeSet[BackupScopeFrpTunnels] {
		tunnels := append([]FrpTunnel(nil), state.FrpTunnels...)
		sort.Slice(tunnels, func(i, j int) bool { return tunnels[i].ID < tunnels[j].ID })
		payload.FrpTunnels = &BackupFrpTunnelsData{Tunnels: tunnels}
		for _, tunnel := range tunnels {
			selectedIDs[tunnel.ID] = true
		}
	}
	if includeLogsAndAudit {
		payload.LogsAndAudit = buildRelatedBackupHistory(state, selectedIDs)
	}
	return payload
}

func BackupPayloadCounts(payload BackupPayload) map[string]int {
	counts := map[string]int{}
	if payload.Projects != nil {
		counts["projects"] = len(payload.Projects.Projects)
		if payload.Projects.ApplicationTemplates != nil {
			counts["applicationTemplates"] = len(payload.Projects.ApplicationTemplates)
		}
		counts["applications"] = len(payload.Projects.Applications)
		counts["applicationDeploymentConfigs"] = len(payload.Projects.ApplicationDeploymentConfigs)
		counts["routes"] = len(payload.Projects.Routes)
		counts["uploadFiles"] = len(payload.Projects.UploadFiles)
	}
	if payload.Servers != nil {
		counts["servers"] = len(payload.Servers.Servers)
		counts["serverRoles"] = len(payload.Servers.ServerRoles)
	}
	if payload.DomainsCertificates != nil {
		counts["domains"] = len(payload.DomainsCertificates.Domains)
		counts["certificates"] = len(payload.DomainsCertificates.Certificates)
		counts["resolvers"] = len(payload.DomainsCertificates.Resolvers)
		counts["ingresses"] = len(payload.DomainsCertificates.Ingresses)
	}
	if payload.SecretAssets != nil {
		counts["secretAssets"] = len(payload.SecretAssets.Assets)
	}
	if payload.FrpTunnels != nil {
		counts["frpTunnels"] = len(payload.FrpTunnels.Tunnels)
	}
	if payload.LogsAndAudit != nil {
		counts["runtimeLogs"] = len(payload.LogsAndAudit.RuntimeLogs)
		counts["operationLogs"] = len(payload.LogsAndAudit.OperationLogs)
	}
	counts["secretMaterials"] = len(payload.SecretMaterials)
	return counts
}

func BackupPayloadSecretRefs(payload BackupPayload) []string {
	logical := payload
	logical.LogsAndAudit = nil
	logical.SecretMaterials = nil
	encoded, err := EncodeObject(logical)
	if err != nil {
		return nil
	}
	refs := map[string]bool{}
	collectSecretRefs(encoded, refs)
	result := make([]string, 0, len(refs))
	for ref := range refs {
		if strings.TrimSpace(ref) != "" {
			result = append(result, ref)
		}
	}
	sort.Strings(result)
	return result
}

func BackupPayloadResourceIDs(payload BackupPayload) []string {
	ids := map[string]bool{}
	if payload.Projects != nil {
		collectObjectIDs(ids, payload.Projects.Projects, payload.Projects.Applications, payload.Projects.ApplicationDeploymentConfigs, payload.Projects.Routes)
	}
	if payload.Servers != nil {
		collectObjectIDs(ids, payload.Servers.Servers, payload.Servers.ServerRoles)
	}
	if payload.DomainsCertificates != nil {
		collectObjectIDs(ids, payload.DomainsCertificates.Domains, payload.DomainsCertificates.Certificates, payload.DomainsCertificates.Resolvers, payload.DomainsCertificates.Ingresses)
	}
	if payload.SecretAssets != nil {
		collectObjectIDs(ids, payload.SecretAssets.Assets)
	}
	if payload.FrpTunnels != nil {
		for _, tunnel := range payload.FrpTunnels.Tunnels {
			if tunnel.ID != "" {
				ids[tunnel.ID] = true
			}
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func FilterBackupCheckReports(reports []CheckReport, resourceIDs []string) []CheckReport {
	selected := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		selected[id] = true
	}
	result := []CheckReport{}
	for _, report := range reports {
		if !isTerminalCheckStatus(report.Status) || !processReportRelated(report, selected) {
			continue
		}
		encoded, err := EncodeObject(report)
		if err != nil {
			continue
		}
		sanitized, ok := sanitizeBackupValue(encoded).(map[string]any)
		if !ok {
			continue
		}
		clean, err := DecodeObject[CheckReport](Object(sanitized))
		if err == nil {
			result = append(result, clean)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func FilterBackupFrpProbeHits(hits []FrpProbeHit, tunnelIDs []string) []FrpProbeHit {
	selected := make(map[string]bool, len(tunnelIDs))
	for _, id := range tunnelIDs {
		selected[id] = true
	}
	result := []FrpProbeHit{}
	for _, hit := range hits {
		if selected[hit.TunnelID] {
			result = append(result, hit)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ReceivedAt != result[j].ReceivedAt {
			return result[i].ReceivedAt < result[j].ReceivedAt
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func isTerminalCheckStatus(status string) bool {
	switch status {
	case CheckStatusPassed, CheckStatusWarning, CheckStatusBlocked, CheckStatusFailed, CheckStatusCancelled:
		return true
	default:
		return false
	}
}

func processReportRelated(report CheckReport, selected map[string]bool) bool {
	if selected[report.Subject.ID] {
		return true
	}
	for _, resource := range report.Resources {
		if selected[resource.ID] {
			return true
		}
	}
	for _, check := range report.Checks {
		if selected[check.Target.ID] {
			return true
		}
	}
	for _, impact := range report.Impacts {
		if selected[impact.Resource.ID] {
			return true
		}
	}
	return false
}

func isBackupScope(scope BackupScope) bool {
	for _, candidate := range backupScopeOrder {
		if candidate == scope {
			return true
		}
	}
	return false
}

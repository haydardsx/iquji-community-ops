package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type State struct {
	Servers                      []Object       `json:"servers"`
	ServerRoles                  []Object       `json:"serverRoles"`
	SecretAssets                 []Object       `json:"secretAssets"`
	FrpTunnels                   []FrpTunnel    `json:"frpTunnels"`
	FrpTunnelRuns                []FrpTunnelRun `json:"frpTunnelRuns"`
	LegacyFrpTunnelSessions      []Object       `json:"frpTunnelSessions,omitempty"`
	Domains                      []Object       `json:"domains"`
	Certificates                 []Object       `json:"certificates"`
	Resolvers                    []Object       `json:"resolvers"`
	Ingresses                    []Object       `json:"ingresses"`
	HealthSnapshots              []Object       `json:"healthSnapshots"`
	MonitoringAgents             []Object       `json:"monitoringAgents"`
	MetricSamples                []Object       `json:"metricSamples"`
	MetricAggregates             []Object       `json:"metricAggregates"`
	SwarmSnapshots               []Object       `json:"swarmSnapshots"`
	Projects                     []Object       `json:"projects"`
	Applications                 []Object       `json:"applications"`
	ApplicationInstances         []Object       `json:"applicationInstances"`
	ApplicationTemplates         []Object       `json:"applicationTemplates"`
	ApplicationDeploymentConfigs []Object       `json:"applicationDeploymentConfigs"`
	Routes                       []Object       `json:"routes"`
	ReleasePlans                 []Object       `json:"releasePlans"`
	RuntimeLogs                  []RuntimeLog   `json:"runtimeLogs"`
	RuntimeLogStreams            []Object       `json:"runtimeLogStreams"`
	BuildLogs                    []Object       `json:"buildLogs"`
	OperationLogs                []OperationLog `json:"operationLogs"`
	JenkinsReleaseRuns           []Object       `json:"jenkinsReleaseRuns"`
	ConfirmationTokens           []Confirmation `json:"confirmationTokens"`
	Tasks                        []Task         `json:"tasks"`
	OperationRuns                []OperationRun `json:"operationRuns"`
	BackupRestoreJobs            []Object       `json:"backupRestoreJobs"`
	LegacyImportExportJobs       []Object       `json:"importExportJobs,omitempty"`

	ApplicationTemplateSeedVersion int `json:"applicationTemplateSeedVersion"`
}

type Object map[string]any

type Task struct {
	ID                  string             `json:"id"`
	Action              string             `json:"action"`
	Status              string             `json:"status"`
	TargetKind          string             `json:"targetKind"`
	TargetID            string             `json:"targetId"`
	TargetName          string             `json:"targetName"`
	LogRef              string             `json:"logRef,omitempty"`
	ConfirmationTokenID string             `json:"confirmationTokenId,omitempty"`
	CancelRequested     bool               `json:"cancelRequested,omitempty"`
	QueueOrder          uint64             `json:"queueOrder,omitempty"`
	QueueResources      []TaskResourceLock `json:"queueResources,omitempty"`
	Attempt             uint64             `json:"attempt,omitempty"`
	ClaimToken          string             `json:"claimToken,omitempty"`
	ClaimOwner          string             `json:"claimOwner,omitempty"`
	LeaseExpiresAt      string             `json:"leaseExpiresAt,omitempty"`
	RequestKey          string             `json:"requestKey,omitempty"`
	RequestDigest       string             `json:"requestDigest,omitempty"`
	Payload             map[string]any     `json:"payload"`
	CreatedAt           string             `json:"createdAt"`
	UpdatedAt           string             `json:"updatedAt"`
}

type Confirmation struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	Token     string `json:"token"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
}

func EmptyState() State {
	return State{
		Servers:                      []Object{},
		ServerRoles:                  []Object{},
		SecretAssets:                 []Object{},
		FrpTunnels:                   []FrpTunnel{},
		FrpTunnelRuns:                []FrpTunnelRun{},
		Domains:                      []Object{},
		Certificates:                 []Object{},
		Resolvers:                    []Object{},
		Ingresses:                    []Object{},
		HealthSnapshots:              []Object{},
		MonitoringAgents:             []Object{},
		MetricSamples:                []Object{},
		MetricAggregates:             []Object{},
		SwarmSnapshots:               []Object{},
		Projects:                     []Object{},
		Applications:                 []Object{},
		ApplicationInstances:         []Object{},
		ApplicationTemplates:         DefaultApplicationTemplates(),
		ApplicationDeploymentConfigs: []Object{},
		Routes:                       []Object{},
		ReleasePlans:                 []Object{},
		RuntimeLogs:                  []RuntimeLog{},
		RuntimeLogStreams:            []Object{},
		BuildLogs:                    []Object{},
		OperationLogs:                []OperationLog{},
		JenkinsReleaseRuns:           []Object{},
		ConfirmationTokens:           []Confirmation{},
		Tasks:                        []Task{},
		OperationRuns:                []OperationRun{},
		BackupRestoreJobs:            []Object{},

		ApplicationTemplateSeedVersion: applicationTemplateSeedVersion,
	}
}

func NormalizeState(data []byte) (State, error) {
	var state State
	if len(strings.TrimSpace(string(data))) == 0 {
		state = EmptyState()
		EnsureBuiltinProject(&state)
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	state.EnsureCollections()
	NormalizeServerConnections(state.Servers)
	EnsureBuiltinProject(&state)
	state.NormalizeLogHistory()
	MigrateLegacyFrpTunnelSessions(&state)
	MigrateLegacyBuildServerReferences(&state)
	MigrateRouteServiceWeights(&state)
	MigrateDomainRedirects(&state)
	MigrateApplicationLogConfiguration(&state)
	MigrateApplicationDeploymentLocations(&state)
	state.StripUserReferences()
	return state, nil
}

func MigrateLegacyBuildServerReferences(state *State) {
	if state == nil {
		return
	}
	for _, config := range state.ApplicationDeploymentConfigs {
		delivery, _ := config["deliveryConfig"].(map[string]any)
		if delivery == nil {
			continue
		}
		if StringValue(config["buildServerId"]) == "" {
			if legacy := StringValue(delivery["buildServerId"]); legacy != "" {
				config["buildServerId"] = legacy
			}
		}
		delete(delivery, "buildServerId")
		normalizeManagedJenkinsPipeline(Object(delivery))
	}
	for _, plan := range state.ReleasePlans {
		buildSnapshot, _ := plan["buildSnapshot"].(map[string]any)
		if buildSnapshot == nil {
			continue
		}
		delivery, _ := buildSnapshot["deliveryConfig"].(map[string]any)
		if StringValue(buildSnapshot["buildServerId"]) == "" {
			if legacy := StringValue(delivery["buildServerId"]); legacy != "" {
				buildSnapshot["buildServerId"] = legacy
			}
		}
		if StringValue(plan["buildServerId"]) == "" {
			plan["buildServerId"] = StringValue(buildSnapshot["buildServerId"])
		}
		delete(delivery, "buildServerId")
		normalizeManagedJenkinsPipeline(Object(delivery))
	}
}

func normalizeManagedJenkinsPipeline(delivery Object) {
	if delivery == nil {
		return
	}
	pipeline, _ := delivery["pipeline"].(map[string]any)
	if pipeline == nil {
		pipeline = Object{}
		delivery["pipeline"] = pipeline
	}
	pipeline["folder"] = ManagedJenkinsFolder
	pipeline["jobName"] = ManagedJenkinsJob
	pipeline["jobTemplate"] = ManagedJenkinsJobTemplate
	pipeline["triggerMode"] = "ops_manual"
}

func (state *State) EnsureCollections() {
	if state.Servers == nil {
		state.Servers = []Object{}
	}
	if state.ServerRoles == nil {
		state.ServerRoles = []Object{}
	}
	if state.SecretAssets == nil {
		state.SecretAssets = []Object{}
	}
	if state.FrpTunnels == nil {
		state.FrpTunnels = []FrpTunnel{}
	}
	if state.FrpTunnelRuns == nil {
		state.FrpTunnelRuns = []FrpTunnelRun{}
	}
	if state.Domains == nil {
		state.Domains = []Object{}
	}
	if state.Certificates == nil {
		state.Certificates = []Object{}
	}
	if state.Resolvers == nil {
		state.Resolvers = []Object{}
	}
	if state.Ingresses == nil {
		state.Ingresses = []Object{}
	}
	if state.HealthSnapshots == nil {
		state.HealthSnapshots = []Object{}
	}
	if state.MonitoringAgents == nil {
		state.MonitoringAgents = []Object{}
	}
	if state.MetricSamples == nil {
		state.MetricSamples = []Object{}
	}
	if state.MetricAggregates == nil {
		state.MetricAggregates = []Object{}
	}
	if state.SwarmSnapshots == nil {
		state.SwarmSnapshots = []Object{}
	}
	if state.Projects == nil {
		state.Projects = []Object{}
	}
	if state.Applications == nil {
		state.Applications = []Object{}
	}
	if state.ApplicationInstances == nil {
		state.ApplicationInstances = []Object{}
	}
	state.ensureApplicationTemplates()
	if state.ApplicationDeploymentConfigs == nil {
		state.ApplicationDeploymentConfigs = []Object{}
	}
	if state.Routes == nil {
		state.Routes = []Object{}
	}
	if state.ReleasePlans == nil {
		state.ReleasePlans = []Object{}
	}
	if state.RuntimeLogs == nil {
		state.RuntimeLogs = []RuntimeLog{}
	}
	if state.RuntimeLogStreams == nil {
		state.RuntimeLogStreams = []Object{}
	}
	if state.BuildLogs == nil {
		state.BuildLogs = []Object{}
	}
	if state.OperationLogs == nil {
		state.OperationLogs = []OperationLog{}
	}
	if state.JenkinsReleaseRuns == nil {
		state.JenkinsReleaseRuns = []Object{}
	}
	if state.ConfirmationTokens == nil {
		state.ConfirmationTokens = []Confirmation{}
	}
	if state.Tasks == nil {
		state.Tasks = []Task{}
	}
	if state.OperationRuns == nil {
		state.OperationRuns = []OperationRun{}
	}
	if state.BackupRestoreJobs == nil {
		state.BackupRestoreJobs = []Object{}
	}
	if len(state.BackupRestoreJobs) == 0 && len(state.LegacyImportExportJobs) > 0 {
		state.BackupRestoreJobs = CloneObjectSliceWithoutUserReferences(state.LegacyImportExportJobs)
	}
	state.LegacyImportExportJobs = nil
}

func (state *State) StripUserReferences() {
	stripObjectSlice(state.Servers)
	stripObjectSlice(state.ServerRoles)
	stripObjectSlice(state.SecretAssets)
	stripObjectSlice(state.LegacyFrpTunnelSessions)
	stripObjectSlice(state.Domains)
	stripObjectSlice(state.Certificates)
	stripObjectSlice(state.Resolvers)
	stripObjectSlice(state.Ingresses)
	stripObjectSlice(state.HealthSnapshots)
	stripObjectSlice(state.MonitoringAgents)
	stripObjectSlice(state.MetricSamples)
	stripObjectSlice(state.MetricAggregates)
	stripObjectSlice(state.SwarmSnapshots)
	stripObjectSlice(state.Projects)
	stripObjectSlice(state.Applications)
	stripObjectSlice(state.ApplicationInstances)
	stripObjectSlice(state.ApplicationDeploymentConfigs)
	stripObjectSlice(state.Routes)
	stripObjectSlice(state.ReleasePlans)
	stripObjectSlice(state.RuntimeLogStreams)
	stripObjectSlice(state.BuildLogs)
	stripObjectSlice(state.JenkinsReleaseRuns)
	stripObjectSlice(state.BackupRestoreJobs)
	for index := range state.Tasks {
		stripAny(state.Tasks[index].Payload)
	}
}

var legacyUserReferenceKeys = [...]string{"actor", "exportedBy", "ownerUserIds", "requestedBy", "userId"}

func CloneObjectWithoutUserReferences(input Object) Object {
	next := Object{}
	for key, value := range input {
		if isLegacyUserReferenceKey(key) {
			continue
		}
		next[key] = cloneWithoutUserReferences(value)
	}
	return next
}

func CloneObjectSliceWithoutUserReferences(input []Object) []Object {
	items := make([]Object, 0, len(input))
	for _, item := range input {
		items = append(items, CloneObjectWithoutUserReferences(item))
	}
	return items
}

func cloneWithoutUserReferences(value any) any {
	switch typed := value.(type) {
	case Object:
		return CloneObjectWithoutUserReferences(typed)
	case map[string]any:
		return CloneObjectWithoutUserReferences(Object(typed))
	case []Object:
		return CloneObjectSliceWithoutUserReferences(typed)
	case []map[string]any:
		items := make([]Object, 0, len(typed))
		for _, item := range typed {
			items = append(items, CloneObjectWithoutUserReferences(Object(item)))
		}
		return items
	case []any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, cloneWithoutUserReferences(item))
		}
		return items
	default:
		return value
	}
}

func stripObjectSlice(items []Object) {
	for _, item := range items {
		stripAny(item)
	}
}

func stripAny(value any) {
	switch typed := value.(type) {
	case Object:
		stripMap(map[string]any(typed))
	case map[string]any:
		stripMap(typed)
	case []any:
		for _, item := range typed {
			stripAny(item)
		}
	case []Object:
		stripObjectSlice(typed)
	}
}

func stripMap(value map[string]any) {
	for _, key := range legacyUserReferenceKeys {
		delete(value, key)
	}
	for _, nested := range value {
		stripAny(nested)
	}
}

func isLegacyUserReferenceKey(key string) bool {
	for _, legacyKey := range legacyUserReferenceKeys {
		if key == legacyKey {
			return true
		}
	}
	return false
}

func Now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func PlusHours(hours int) string {
	return time.Now().UTC().Add(time.Duration(hours) * time.Hour).Format(time.RFC3339Nano)
}

func NewID(prefix string) string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return prefix + "_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	}
	return prefix + "_" + hex.EncodeToString(bytes[:])
}

func HashSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TaskResponse(task Task) map[string]any {
	target := NormalizedTaskTarget(task)
	status := task.Status
	switch status {
	case "pending_confirmation", "queued":
		status = "queued"
	case "preflight_running", "executing", "verifying":
		status = "running"
	case "rolled_back":
		status = "failed"
	}
	result := map[string]any{
		"id":     task.ID,
		"action": task.Action,
		"status": status,
		"target": map[string]any{
			"kind": target.Kind,
			"id":   target.ID,
			"name": target.Name,
		},
		"createdAt": task.CreatedAt,
		"updatedAt": task.UpdatedAt,
		"traceId":   NewID("trace"),
	}
	if task.LogRef != "" {
		result["logRef"] = task.LogRef
	}
	return result
}

package domain

import (
	"fmt"
	"sort"
	"strings"
)

type FrpTunnelStatus string

const (
	FrpTunnelStatusPendingConfirmation FrpTunnelStatus = "pending_confirmation"
	FrpTunnelStatusStarting            FrpTunnelStatus = "starting"
	FrpTunnelStatusRunning             FrpTunnelStatus = "running"
	FrpTunnelStatusStopping            FrpTunnelStatus = "stopping"
	FrpTunnelStatusStopped             FrpTunnelStatus = "stopped"
	FrpTunnelStatusFailed              FrpTunnelStatus = "failed"
	FrpTunnelStatusCleanupRequired     FrpTunnelStatus = "cleanup_required"
	FrpTunnelStatusCancelled           FrpTunnelStatus = "cancelled"
)

type FrpTunnelMappingTarget string

const (
	FrpTunnelMappingTargetLocalService FrpTunnelMappingTarget = "local_service"
	FrpTunnelMappingTargetHTTPProbe    FrpTunnelMappingTarget = "http_probe"
)

type FrpTunnelMapping struct {
	Name             string                 `json:"name"`
	TargetType       FrpTunnelMappingTarget `json:"targetType"`
	LocalHost        string                 `json:"localHost"`
	LocalPort        int                    `json:"localPort"`
	FrpsProxyPort    int                    `json:"frpsProxyPort"`
	PublicDomain     string                 `json:"publicDomain,omitempty"`
	PublicPathPrefix string                 `json:"publicPathPrefix"`
	Entrypoint       string                 `json:"entrypoint"`
	PublicScheme     string                 `json:"publicScheme"`
}

type FrpTunnelMappingInput struct {
	TargetType       FrpTunnelMappingTarget `json:"targetType"`
	Name             string                 `json:"name"`
	LocalHost        string                 `json:"localHost,omitempty"`
	LocalPort        int                    `json:"localPort,omitempty"`
	FrpsProxyPort    int                    `json:"frpsProxyPort"`
	PublicDomain     string                 `json:"publicDomain,omitempty"`
	PublicPathPrefix string                 `json:"publicPathPrefix,omitempty"`
	Entrypoint       string                 `json:"entrypoint,omitempty"`
	PublicScheme     string                 `json:"publicScheme,omitempty"`
}

type FrpTunnelInput struct {
	Name         string                  `json:"name"`
	ServerID     string                  `json:"serverId"`
	FrpsBindPort int                     `json:"frpsBindPort,omitempty"`
	Mappings     []FrpTunnelMappingInput `json:"mappings"`
}

type FrpTunnel struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	ServerID     string             `json:"serverId"`
	FrpsBindPort int                `json:"frpsBindPort"`
	Mappings     []FrpTunnelMapping `json:"mappings"`
	CreatedAt    string             `json:"createdAt"`
	UpdatedAt    string             `json:"updatedAt"`
}

type FrpTunnelRun struct {
	ID                  string             `json:"id"`
	TunnelID            string             `json:"tunnelId"`
	ServerID            string             `json:"serverId"`
	Status              FrpTunnelStatus    `json:"status"`
	FrpsBindPort        int                `json:"frpsBindPort"`
	FrpsImage           string             `json:"frpsImage"`
	FrpVersion          string             `json:"frpVersion,omitempty"`
	Mappings            []FrpTunnelMapping `json:"mappings"`
	TokenSecretRef      string             `json:"tokenSecretRef,omitempty"`
	ContainerName       string             `json:"containerName"`
	RemoteDir           string             `json:"remoteDir"`
	ManagedFrps         bool               `json:"managedFrps,omitempty"`
	StartOperationRunID string             `json:"startOperationRunId,omitempty"`
	StopOperationRunID  string             `json:"stopOperationRunId,omitempty"`
	ErrorSummary        string             `json:"errorSummary,omitempty"`
	StartedAt           string             `json:"startedAt,omitempty"`
	StoppedAt           string             `json:"stoppedAt,omitempty"`
	FailedAt            string             `json:"failedAt,omitempty"`
	LastVerifiedAt      string             `json:"lastVerifiedAt,omitempty"`
	CreatedAt           string             `json:"createdAt"`
	UpdatedAt           string             `json:"updatedAt"`
}

type FrpProbeHit struct {
	Event       string `json:"event"`
	Source      string `json:"source"`
	Level       string `json:"level"`
	Message     string `json:"message"`
	Timestamp   string `json:"timestamp"`
	ID          string `json:"id"`
	TunnelID    string `json:"tunnelId"`
	RunID       string `json:"runId"`
	MappingName string `json:"mappingName"`
	Method      string `json:"method"`
	Host        string `json:"host,omitempty"`
	Path        string `json:"path"`
	RawQuery    string `json:"rawQuery,omitempty"`
	RemoteAddr  string `json:"remoteAddr"`
	UserAgent   string `json:"userAgent,omitempty"`
	ReceivedAt  string `json:"receivedAt"`
}

func (status FrpTunnelStatus) IsActive() bool {
	switch status {
	case FrpTunnelStatusPendingConfirmation, FrpTunnelStatusStarting, FrpTunnelStatusRunning, FrpTunnelStatusStopping, FrpTunnelStatusCleanupRequired:
		return true
	default:
		return false
	}
}

func (status FrpTunnelStatus) NeedsCleanup() bool {
	switch status {
	case FrpTunnelStatusStarting, FrpTunnelStatusRunning, FrpTunnelStatusStopping, FrpTunnelStatusCleanupRequired:
		return true
	default:
		return false
	}
}

func FindFrpTunnel(items []FrpTunnel, id string) *FrpTunnel {
	for index := range items {
		if items[index].ID == id {
			return &items[index]
		}
	}
	return nil
}

func FindFrpTunnelRun(items []FrpTunnelRun, id string) *FrpTunnelRun {
	for index := range items {
		if items[index].ID == id {
			return &items[index]
		}
	}
	return nil
}

func FrpTunnelRunsFor(items []FrpTunnelRun, tunnelID string) []FrpTunnelRun {
	result := make([]FrpTunnelRun, 0)
	for _, run := range items {
		if run.TunnelID == tunnelID {
			result = append(result, run)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})
	return result
}

func LatestFrpTunnelRun(items []FrpTunnelRun, tunnelID string) *FrpTunnelRun {
	runs := FrpTunnelRunsFor(items, tunnelID)
	if len(runs) == 0 {
		return nil
	}
	return &runs[0]
}

func ActiveFrpTunnelRunForServer(items []FrpTunnelRun, serverID string, exceptRunID string) *FrpTunnelRun {
	for index := range items {
		run := &items[index]
		if run.ID != exceptRunID && run.ServerID == serverID && run.Status.IsActive() {
			return run
		}
	}
	return nil
}

func MigrateLegacyFrpTunnelSessions(state *State) {
	if state == nil || len(state.LegacyFrpTunnelSessions) == 0 {
		return
	}
	existingTunnels := map[string]bool{}
	existingRuns := map[string]bool{}
	for _, tunnel := range state.FrpTunnels {
		existingTunnels[tunnel.ID] = true
	}
	for _, run := range state.FrpTunnelRuns {
		existingRuns[run.ID] = true
	}
	for _, legacy := range state.LegacyFrpTunnelSessions {
		legacyID := StringValue(legacy["id"])
		if legacyID == "" {
			continue
		}
		mappings := legacyFrpTunnelMappings(legacy["mappings"])
		createdAt := StringValue(legacy["createdAt"])
		if createdAt == "" {
			createdAt = Now()
		}
		updatedAt := StringValue(legacy["updatedAt"])
		if updatedAt == "" {
			updatedAt = createdAt
		}
		if !existingTunnels[legacyID] {
			state.FrpTunnels = append(state.FrpTunnels, FrpTunnel{
				ID:           legacyID,
				Name:         legacyFrpTunnelName(legacy, state.Servers),
				ServerID:     StringValue(legacy["serverId"]),
				FrpsBindPort: NumberOrDefault(legacy["frpsBindPort"], 7000),
				Mappings:     mappings,
				CreatedAt:    createdAt,
				UpdatedAt:    updatedAt,
			})
			existingTunnels[legacyID] = true
		}
		if !existingRuns[legacyID] {
			state.FrpTunnelRuns = append(state.FrpTunnelRuns, FrpTunnelRun{
				ID:                  legacyID,
				TunnelID:            legacyID,
				ServerID:            StringValue(legacy["serverId"]),
				Status:              legacyFrpTunnelStatus(legacy["status"]),
				FrpsBindPort:        NumberOrDefault(legacy["frpsBindPort"], 7000),
				FrpsImage:           StringValue(legacy["frpsImage"]),
				FrpVersion:          StringValue(legacy["frpVersion"]),
				Mappings:            mappings,
				TokenSecretRef:      StringValue(legacy["tokenSecretRef"]),
				ContainerName:       StringValue(legacy["containerName"]),
				RemoteDir:           StringValue(legacy["remoteDir"]),
				StartOperationRunID: StringValue(legacy["taskId"]),
				StopOperationRunID:  StringValue(legacy["stopTaskId"]),
				ErrorSummary:        StringValue(legacy["errorSummary"]),
				StartedAt:           StringValue(legacy["startedAt"]),
				StoppedAt:           StringValue(legacy["stoppedAt"]),
				FailedAt:            StringValue(legacy["failedAt"]),
				LastVerifiedAt:      StringValue(legacy["lastVerifiedAt"]),
				CreatedAt:           createdAt,
				UpdatedAt:           updatedAt,
			})
			existingRuns[legacyID] = true
		}
	}
	state.LegacyFrpTunnelSessions = nil
}

func legacyFrpTunnelMappings(value any) []FrpTunnelMapping {
	objects := ObjectSliceFromAny(value)
	result := make([]FrpTunnelMapping, 0, len(objects))
	for _, item := range objects {
		name := StringValue(item["name"])
		localPort := NumberValue(item["localPort"])
		target := FrpTunnelMappingTargetLocalService
		if localPort == 17990 || strings.HasPrefix(name, "probe") {
			target = FrpTunnelMappingTargetHTTPProbe
		}
		proxyPort := NumberValue(item["frpsProxyPort"])
		if proxyPort == 0 {
			proxyPort = NumberValue(item["remotePort"])
		}
		path := StringValue(item["publicPathPrefix"])
		if path == "" {
			path = "/"
		}
		entrypoint := StringValue(item["entrypoint"])
		if entrypoint == "" {
			entrypoint = "web"
		}
		scheme := StringValue(item["publicScheme"])
		if scheme == "" {
			if entrypoint == "websecure" {
				scheme = "https"
			} else {
				scheme = "http"
			}
		}
		result = append(result, FrpTunnelMapping{
			Name:             name,
			TargetType:       target,
			LocalHost:        StringValue(item["localHost"]),
			LocalPort:        localPort,
			FrpsProxyPort:    proxyPort,
			PublicDomain:     StringValue(item["publicDomain"]),
			PublicPathPrefix: path,
			Entrypoint:       entrypoint,
			PublicScheme:     scheme,
		})
	}
	return result
}

func legacyFrpTunnelStatus(value any) FrpTunnelStatus {
	status := FrpTunnelStatus(StringValue(value))
	switch status {
	case FrpTunnelStatusPendingConfirmation, FrpTunnelStatusStarting, FrpTunnelStatusRunning, FrpTunnelStatusStopping, FrpTunnelStatusStopped, FrpTunnelStatusFailed, FrpTunnelStatusCleanupRequired, FrpTunnelStatusCancelled:
		return status
	default:
		return FrpTunnelStatusStopped
	}
}

func legacyFrpTunnelName(session Object, servers []Object) string {
	if name := strings.TrimSpace(StringValue(session["name"])); name != "" {
		return name
	}
	serverName := "Tunnel"
	if server := FindObject(servers, StringValue(session["serverId"])); server != nil {
		serverName = StringValue(server["name"])
		if serverName == "" {
			serverName = "Tunnel"
		}
	}
	id := StringValue(session["id"])
	if len(id) > 8 {
		id = id[len(id)-8:]
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s", serverName, id))
}

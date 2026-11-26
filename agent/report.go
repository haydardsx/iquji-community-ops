package main

import "time"

type metricSample struct {
	TargetKind string             `json:"targetKind"`
	TargetID   string             `json:"targetId,omitempty"`
	SampledAt  string             `json:"sampledAt,omitempty"`
	Values     map[string]float64 `json:"values"`
	Labels     map[string]string  `json:"labels,omitempty"`
}

type swarmNode struct {
	ID             string `json:"id"`
	ServerID       string `json:"serverId,omitempty"`
	Role           string `json:"role,omitempty"`
	Hostname       string `json:"hostname"`
	Status         string `json:"status"`
	Availability   string `json:"availability"`
	ManagerStatus  string `json:"managerStatus,omitempty"`
	EngineVersion  string `json:"engineVersion,omitempty"`
	AdvertiseAddr  string `json:"advertiseAddr,omitempty"`
	ManagerAddress string `json:"managerAddress,omitempty"`
	ConnectionMode string `json:"connectionMode,omitempty"`
}

type swarmService struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Image    string `json:"image"`
	Replicas string `json:"replicas"`
}

type swarmTask struct {
	ID           string `json:"id"`
	ServiceID    string `json:"serviceId"`
	ServiceName  string `json:"serviceName"`
	NodeID       string `json:"nodeId"`
	DesiredState string `json:"desiredState"`
	CurrentState string `json:"currentState"`
	Image        string `json:"image"`
	Error        string `json:"error"`
}

type swarmNetwork struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Scope  string `json:"scope"`
}

type swarmSnapshot struct {
	ControlAvailable bool           `json:"controlAvailable"`
	ClusterID        string         `json:"clusterId,omitempty"`
	NodeID           string         `json:"nodeId,omitempty"`
	Role             string         `json:"role,omitempty"`
	LocalNodeState   string         `json:"localNodeState,omitempty"`
	ErrorCode        string         `json:"errorCode,omitempty"`
	Nodes            []swarmNode    `json:"nodes"`
	Services         []swarmService `json:"services"`
	Tasks            []swarmTask    `json:"tasks"`
	Networks         []swarmNetwork `json:"networks"`
	Summary          string         `json:"summary"`
	Status           string         `json:"status"`
}

type report struct {
	ServerID        string         `json:"serverId"`
	Version         string         `json:"version"`
	ProtocolVersion int            `json:"protocolVersion"`
	SampledAt       string         `json:"sampledAt"`
	Capabilities    []string       `json:"capabilities"`
	Samples         []metricSample `json:"samples"`
	SwarmSnapshot   *swarmSnapshot `json:"swarmSnapshot,omitempty"`
	LastError       string         `json:"lastError,omitempty"`
}

func collect(cfg config) report {
	sampledAt := time.Now().UTC().Format(time.RFC3339Nano)
	dockerReady := commandAvailable("docker", "version")
	var snapshot *swarmSnapshot
	if dockerReady {
		value := collectSwarmSnapshot()
		snapshot = &value
	}

	samples := []metricSample{
		{
			TargetKind: "server",
			TargetID:   cfg.ServerID,
			SampledAt:  sampledAt,
			Values: map[string]float64{
				"cpuPercent":      cpuPercent(),
				"memoryPercent":   memoryPercent(),
				"diskPercent":     diskPercent(),
				"dockerDiskBytes": dockerDiskBytes(),
			},
			Labels: map[string]string{"serverId": cfg.ServerID},
		},
	}
	if dockerReady {
		samples = append(samples, collectContainers(sampledAt)...)
	}

	capabilities := []string{"host"}
	if dockerReady {
		capabilities = append(capabilities, "docker")
	}
	if snapshot != nil && snapshot.LocalNodeState == "active" {
		capabilities = append(capabilities, "swarm")
	}

	return report{
		ServerID:        cfg.ServerID,
		Version:         cfg.Version,
		ProtocolVersion: cfg.ProtocolVersion,
		SampledAt:       sampledAt,
		Capabilities:    capabilities,
		Samples:         samples,
		SwarmSnapshot:   snapshot,
	}
}

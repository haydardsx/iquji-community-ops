package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
)

func collectContainers(sampledAt string) []metricSample {
	containers := dockerJSONRows("ps", "-a", "--format", "{{json .}}")
	stats := dockerJSONRows("stats", "--no-stream", "--format", "{{json .}}")
	statsByName := map[string]dockerRow{}
	for _, stat := range stats {
		name := stat.First("Name", "Container")
		if name != "" {
			statsByName[name] = stat
		}
	}

	var samples []metricSample
	for _, container := range containers {
		name := container.First("Names", "Name", "ID")
		stat, ok := statsByName[name]
		if !ok {
			stat = dockerRow{}
		}
		samples = append(samples, metricSample{
			TargetKind: "container",
			TargetID:   name,
			SampledAt:  sampledAt,
			Values: map[string]float64{
				"cpuPercent":    parsePercent(stat.String("CPUPerc")),
				"memoryBytes":   parseBytes(stat.String("MemUsage")),
				"memoryPercent": parsePercent(stat.String("MemPerc")),
				"restartCount":  0,
			},
			Labels: map[string]string{
				"containerName":      name,
				"image":              firstNonEmpty(container.String("Image"), stat.String("Image")),
				"status":             container.String("Status"),
				"composeProjectName": labelValue(container.String("Labels"), "com.docker.compose.project"),
			},
		})
	}
	return samples
}

func collectSwarmSnapshot() swarmSnapshot {
	return collectSwarmWithRunner(run)
}

func collectSwarmWithRunner(execute func(string, ...string) (string, error)) swarmSnapshot {
	snapshot := swarmSnapshot{
		Nodes: []swarmNode{}, Services: []swarmService{}, Tasks: []swarmTask{}, Networks: []swarmNetwork{},
		Status: "error", ErrorCode: "SWARM_PROBE_FAILED",
	}
	output, err := execute("docker", "info", "--format", "{{json .Swarm}}")
	var info struct {
		NodeID           string
		LocalNodeState   string
		ControlAvailable bool
		Cluster          struct{ ID string }
	}
	if err != nil || json.Unmarshal([]byte(output), &info) != nil {
		return snapshot
	}
	snapshot.NodeID, snapshot.ClusterID, snapshot.LocalNodeState = info.NodeID, info.Cluster.ID, info.LocalNodeState
	snapshot.ControlAvailable = info.ControlAvailable
	snapshot.ErrorCode = ""
	if info.LocalNodeState == "inactive" {
		snapshot.Status, snapshot.Summary = "not_enabled", "Swarm 未启用"
		return snapshot
	}
	if info.LocalNodeState != "active" {
		snapshot.ErrorCode, snapshot.Summary = "SWARM_NODE_NOT_ACTIVE", "Swarm 节点状态异常"
		return snapshot
	}
	snapshot.Role, snapshot.Status = "worker", "success"
	if !info.ControlAvailable {
		hostname, _ := os.Hostname()
		snapshot.Nodes = append(snapshot.Nodes, swarmNode{ID: info.NodeID, Hostname: hostname, Role: "worker", Status: "unknown"})
		snapshot.Summary = "已采集本机 Swarm worker 身份"
		return snapshot
	}
	snapshot.Role = "manager"
	readRows := func(args ...string) []dockerRow {
		output, err := execute("docker", args...)
		if err != nil {
			snapshot.Status, snapshot.ErrorCode = "error", "SWARM_SNAPSHOT_INCOMPLETE"
			return nil
		}
		rows, err := parseSwarmJSONRows(output)
		if err != nil {
			snapshot.Status, snapshot.ErrorCode = "error", "SWARM_SNAPSHOT_INVALID"
		}
		return rows
	}
	for _, row := range readRows("node", "ls", "--format", "{{json .}}") {
		role := "worker"
		if row.String("ManagerStatus") != "" {
			role = "manager"
		}
		snapshot.Nodes = append(snapshot.Nodes, swarmNode{
			ID: row.First("ID", "Id"), Hostname: row.First("Hostname", "Name", "ID"), Role: role,
			Status: row.String("Status"), Availability: row.String("Availability"),
			ManagerStatus: row.String("ManagerStatus"), EngineVersion: row.String("EngineVersion"),
		})
	}
	if len(snapshot.Nodes) > 0 {
		args := []string{"node", "inspect"}
		for _, node := range snapshot.Nodes {
			args = append(args, node.ID)
		}
		output, err := execute("docker", args...)
		var inspected []struct {
			ID            string
			Spec          struct{ Labels map[string]string }
			Status        struct{ Addr string }
			ManagerStatus *struct{ Addr string }
		}
		if err == nil && json.Unmarshal([]byte(output), &inspected) == nil {
			for i := range snapshot.Nodes {
				for _, node := range inspected {
					if node.ID == snapshot.Nodes[i].ID {
						snapshot.Nodes[i].ServerID = node.Spec.Labels["iquops.server-id"]
						address := node.Status.Addr
						if node.ManagerStatus != nil {
							snapshot.Nodes[i].ManagerAddress = node.ManagerStatus.Addr
							if host, _, err := net.SplitHostPort(node.ManagerStatus.Addr); err == nil {
								address = host
							}
						}
						// manager 节点记录包含真实通告地址，避免用 NAT 下的本机网卡地址推断。
						ip := net.ParseIP(address)
						if ip != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
							snapshot.Nodes[i].AdvertiseAddr = ip.String()
							mode := "public"
							v4 := ip.To4()
							if ip.IsPrivate() || v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
								mode = "private"
							}
							snapshot.Nodes[i].ConnectionMode = mode
						}
					}
				}
			}
		}
	}
	for _, row := range readRows("service", "ls", "--format", "{{json .}}") {
		service := swarmService{ID: row.First("ID", "Name"), Name: row.First("Name", "ID"), Image: row.String("Image"), Replicas: row.String("Replicas")}
		snapshot.Services = append(snapshot.Services, service)
		for _, task := range readRows("service", "ps", "--no-trunc", service.ID, "--format", "{{json .}}") {
			snapshot.Tasks = append(snapshot.Tasks, swarmTask{
				ID: task.First("ID", "Name"), ServiceID: service.ID, ServiceName: service.Name,
				NodeID: task.String("Node"), DesiredState: task.String("DesiredState"), CurrentState: task.String("CurrentState"),
				Image: firstNonEmpty(task.String("Image"), service.Image), Error: task.String("Error"),
			})
		}
	}
	for _, row := range readRows("network", "ls", "--filter", "driver=overlay", "--format", "{{json .}}") {
		snapshot.Networks = append(snapshot.Networks, swarmNetwork{
			ID: row.First("ID", "Name"), Name: row.First("Name", "ID"), Driver: firstNonEmpty(row.String("Driver"), "overlay"), Scope: row.String("Scope"),
		})
	}
	for _, task := range snapshot.Tasks {
		text := strings.ToLower(task.CurrentState + " " + task.Error)
		if snapshot.Status == "success" && (strings.Contains(text, "failed") || strings.Contains(text, "rejected")) {
			snapshot.Status = "warning"
		}
	}
	snapshot.Summary = fmt.Sprintf("nodes=%d services=%d tasks=%d", len(snapshot.Nodes), len(snapshot.Services), len(snapshot.Tasks))
	return snapshot
}

func parseSwarmJSONRows(output string) ([]dockerRow, error) {
	rows := []dockerRow{}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row dockerRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

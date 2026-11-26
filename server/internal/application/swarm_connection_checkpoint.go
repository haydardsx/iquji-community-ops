package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"reflect"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type swarmConnectionCheckpoint struct {
	NodeID          string               `json:"nodeId"`
	ClusterID       string               `json:"clusterId"`
	Local           swarmLocalConnection `json:"local"`
	ClusterSpecHash string               `json:"clusterSpecHash"`
	DataPathPort    uint32               `json:"dataPathPort"`
	DefaultAddrPool []string             `json:"defaultAddrPool"`
	SubnetSize      uint32               `json:"subnetSize"`
	Availability    string               `json:"availability"`
	ServiceHashes   map[string]string    `json:"serviceHashes"`
	Running         map[string]int       `json:"running"`
	Networks        map[string]bool      `json:"networks"`
	Nodes           map[string]bool      `json:"nodes"`
}

// 任务中只保存地址、身份、配置摘要和资源基线，完整 Spec 始终留在内存。
func prepareSwarmConnectionCheckpoint(ctx context.Context, input ServerRuntimeExecutionInput, current swarmEngineBaseline) (swarmEngineBaseline, error) {
	checkpoint := swarmConnectionCheckpoint{}
	if len(input.SwarmConnectionCheckpoint) == 0 {
		checkpoint = swarmConnectionCheckpoint{
			NodeID: current.NodeID, ClusterID: current.ClusterID, Local: current.Local,
			ClusterSpecHash: swarmValueHash(current.Init["Spec"]), DataPathPort: current.Init["DataPathPort"].(uint32),
			DefaultAddrPool: effectiveSwarmDefaultAddrPool(current.Init["DefaultAddrPool"].([]string)), SubnetSize: current.Init["SubnetSize"].(uint32), Availability: current.Init["Availability"].(string),
			ServiceHashes: swarmServiceHashes(current.Services), Running: current.Running, Networks: current.Networks, Nodes: current.Nodes,
		}
		if input.SaveSwarmConnectionCheckpoint != nil {
			raw, _ := json.Marshal(checkpoint)
			var object domain.Object
			if json.Unmarshal(raw, &object) != nil {
				return current, errors.New("SWARM_CONNECTION_CHECKPOINT_FAILED")
			}
			if err := input.SaveSwarmConnectionCheckpoint(ctx, object); err != nil {
				return current, swarmOperationError(ctx, err, "SWARM_CONNECTION_CHECKPOINT_FAILED")
			}
		}
		return current, nil
	}
	raw, err := json.Marshal(input.SwarmConnectionCheckpoint)
	if err != nil || json.Unmarshal(raw, &checkpoint) != nil || checkpoint.NodeID == "" || checkpoint.ClusterID == "" || checkpoint.ClusterSpecHash == "" || checkpoint.ServiceHashes == nil || checkpoint.Running == nil || checkpoint.Nodes == nil || checkpoint.Networks == nil {
		return current, errors.New("SWARM_CONNECTION_RECOVERY_REQUIRED")
	}
	if checkpoint.NodeID != current.NodeID || checkpoint.ClusterID != current.ClusterID || checkpoint.ClusterSpecHash != swarmValueHash(current.Init["Spec"]) ||
		!reflect.DeepEqual(checkpoint.ServiceHashes, swarmServiceHashes(current.Services)) || !reflect.DeepEqual(checkpoint.Networks, current.Networks) || !reflect.DeepEqual(checkpoint.Nodes, current.Nodes) ||
		checkpoint.DataPathPort != current.Init["DataPathPort"] || checkpoint.SubnetSize != current.Init["SubnetSize"] || !reflect.DeepEqual(effectiveSwarmDefaultAddrPool(checkpoint.DefaultAddrPool), effectiveSwarmDefaultAddrPool(current.Init["DefaultAddrPool"].([]string))) || checkpoint.Availability != current.Init["Availability"] {
		return current, errors.New("SWARM_CONNECTION_RECOVERY_REQUIRED")
	}
	if _, err := normalizeSwarmManagerEndpoint(checkpoint.Local.AdvertiseAddr); err != nil || checkpoint.Local.ListenAddr == "" {
		return current, errors.New("SWARM_CONNECTION_RECOVERY_REQUIRED")
	}
	current.Local = checkpoint.Local
	current.Init = maps.Clone(current.Init)
	current.Init["ListenAddr"], current.Init["AdvertiseAddr"], current.Init["DataPathAddr"] = checkpoint.Local.ListenAddr, checkpoint.Local.AdvertiseAddr, checkpoint.Local.DataPathAddr
	current.Running = checkpoint.Running
	return current, nil
}

func canonicalSwarmJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func swarmValueHash(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	raw, err = canonicalSwarmJSON(raw)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func swarmServiceHashes(services map[string]string) map[string]string {
	result := map[string]string{}
	for id, spec := range services {
		result[id] = swarmValueHash(json.RawMessage(spec))
	}
	return result
}

package application

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type swarmEngineRequest func(context.Context, string, string, any) (json.RawMessage, error)

// HTTP 请求体只经 SSH 标准输入传给正式 Docker API，避免 Spec 和凭据出现在命令或日志中。
func swarmEngineOverSSH(client sshSessionClient) swarmEngineRequest {
	return func(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var payload []byte
		var err error
		if body != nil {
			payload, err = json.Marshal(body)
			if err != nil {
				return nil, errors.New("SWARM_ENGINE_API_UNAVAILABLE")
			}
		}
		request, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, bytes.NewReader(payload))
		if err != nil {
			return nil, errors.New("SWARM_ENGINE_API_UNAVAILABLE")
		}
		request.Close = true
		request.Header.Set("Content-Type", "application/json")
		var wire bytes.Buffer
		if request.Write(&wire) != nil {
			return nil, errors.New("SWARM_ENGINE_API_UNAVAILABLE")
		}
		session, err := client.NewSession()
		if err != nil {
			return nil, errors.New("SWARM_ENGINE_API_UNAVAILABLE")
		}
		defer func() { _ = session.Close() }()
		output := &cappedLogBuffer{limit: 8 << 20}
		session.Stdin = &wire
		session.Stdout = output
		session.Stderr = io.Discard
		done := make(chan error, 1)
		go func() { done <- session.Run("docker system dial-stdio") }()
		select {
		case err = <-done:
			if err != nil {
				return nil, swarmOperationError(ctx, err, "SWARM_ENGINE_API_UNAVAILABLE")
			}
		case <-ctx.Done():
			_ = session.Signal(ssh.SIGTERM)
			return nil, ctx.Err()
		}
		response, err := http.ReadResponse(bufio.NewReader(strings.NewReader(output.String())), request)
		if err != nil {
			return nil, errors.New("SWARM_ENGINE_API_UNAVAILABLE")
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		if err != nil {
			return nil, errors.New("SWARM_ENGINE_API_UNAVAILABLE")
		}
		if response.StatusCode >= 300 {
			if method == http.MethodPost && path == "/swarm/join" {
				var failure struct{ Message string }
				_ = json.Unmarshal(data, &failure)
				return nil, errors.New(swarmJoinFailureCode(failure.Message))
			}
			return nil, errors.New("SWARM_ENGINE_API_UNAVAILABLE")
		}
		return json.RawMessage(data), nil
	}
}

func swarmConnectionInitBody(parameters map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(parameters["Spec"])
	var spec map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &spec) != nil || spec == nil {
		return nil, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
	}
	if rawCA, present := spec["CAConfig"]; present {
		var ca map[string]json.RawMessage
		if json.Unmarshal(rawCA, &ca) != nil {
			return nil, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
		}
		// Moby 将 ExternalCAs 追加到原配置，省略此字段才能保留既有列表。
		delete(ca, "ExternalCAs")
		spec["CAConfig"], err = json.Marshal(ca)
		if err != nil {
			return nil, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
		}
	}
	result := maps.Clone(parameters)
	encoded, err := json.Marshal(spec)
	if err != nil {
		return nil, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
	}
	result["Spec"] = json.RawMessage(encoded)
	return result, nil
}

type swarmLocalConnection struct {
	ListenAddr    string
	AdvertiseAddr string
	DataPathAddr  string
}

type swarmEngineBaseline struct {
	ReadLocal func(context.Context) (swarmLocalConnection, error)
	NodeID    string
	ClusterID string
	Local     swarmLocalConnection
	Init      map[string]any
	Services  map[string]string
	Running   map[string]int
	Networks  map[string]bool
	Nodes     map[string]bool
}

func readSwarmLocalConnection(ctx context.Context, client sshSessionClient) (swarmLocalConnection, error) {
	root, err := remoteOutput(ctx, client, "docker info --format '{{.DockerRootDir}}'")
	if err != nil || !strings.HasPrefix(root, "/") {
		return swarmLocalConnection{}, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
	}
	output, err := remoteOutput(ctx, client, "cat -- "+shellQuote(strings.TrimRight(root, "/")+"/swarm/docker-state.json"))
	var result swarmLocalConnection
	if err != nil || json.Unmarshal([]byte(output), &result) != nil || result.ListenAddr == "" || result.AdvertiseAddr == "" {
		return result, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
	}
	if _, err := normalizeSwarmManagerEndpoint(result.AdvertiseAddr); err != nil {
		return result, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
	}
	return result, nil
}

func readSwarmEngineBaseline(ctx context.Context, call swarmEngineRequest, nodeID string, local swarmLocalConnection) (swarmEngineBaseline, error) {
	baseline := swarmEngineBaseline{NodeID: nodeID, Local: local}
	raw, err := call(ctx, http.MethodGet, "/swarm", nil)
	if err != nil {
		return baseline, err
	}
	var cluster struct {
		ID              string
		Spec            json.RawMessage
		DataPathPort    uint32
		DefaultAddrPool []string
		SubnetSize      uint32
	}
	if json.Unmarshal(raw, &cluster) != nil || cluster.ID == "" || len(cluster.Spec) == 0 {
		return baseline, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
	}
	var security struct {
		EncryptionConfig struct{ AutoLockManagers bool }
	}
	if json.Unmarshal(cluster.Spec, &security) != nil {
		return baseline, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
	}
	if security.EncryptionConfig.AutoLockManagers {
		return baseline, errors.New("SWARM_CONNECTION_AUTOLOCK_UNSUPPORTED")
	}
	baseline.ClusterID = cluster.ID
	raw, err = call(ctx, http.MethodGet, "/nodes", nil)
	if err != nil {
		return baseline, err
	}
	var nodes []struct {
		ID   string
		Spec struct {
			Role         string
			Availability string
		}
	}
	if json.Unmarshal(raw, &nodes) != nil {
		return baseline, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
	}
	managers, availability := 0, ""
	baseline.Nodes = map[string]bool{}
	for _, node := range nodes {
		baseline.Nodes[node.ID] = true
		if node.Spec.Role == "manager" {
			managers++
		}
		if node.ID == nodeID && node.Spec.Role == "manager" {
			availability = node.Spec.Availability
		}
	}
	if managers != 1 {
		return baseline, errors.New("SWARM_CONNECTION_MULTIPLE_MANAGERS")
	}
	if availability == "" {
		return baseline, errors.New("SWARM_CONNECTION_MANAGER_REQUIRED")
	}
	baseline.Init = map[string]any{
		"ForceNewCluster": true, "ListenAddr": local.ListenAddr, "AdvertiseAddr": local.AdvertiseAddr, "DataPathAddr": local.DataPathAddr,
		"AutoLockManagers": security.EncryptionConfig.AutoLockManagers, "Spec": cluster.Spec, "DataPathPort": cluster.DataPathPort, "DefaultAddrPool": effectiveSwarmDefaultAddrPool(cluster.DefaultAddrPool), "SubnetSize": cluster.SubnetSize, "Availability": availability,
	}
	baseline.Services, baseline.Running, baseline.Networks, err = readSwarmEngineResources(ctx, call)
	return baseline, err
}

// Docker 未显式记录地址池时使用内置默认池，强制初始化后可能补写该值。
func effectiveSwarmDefaultAddrPool(pool []string) []string {
	if len(pool) == 0 {
		return []string{"10.0.0.0/8"}
	}
	return pool
}

func readSwarmEngineResources(ctx context.Context, call swarmEngineRequest) (map[string]string, map[string]int, map[string]bool, error) {
	services, running, networks := map[string]string{}, map[string]int{}, map[string]bool{}
	for _, kind := range []string{"services", "tasks", "networks"} {
		path := "/" + kind
		if kind == "networks" {
			path += "?filters=%7B%22scope%22%3A%5B%22swarm%22%5D%7D"
		}
		raw, err := call(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, nil, nil, err
		}
		var items []struct {
			ID           string
			ServiceID    string
			DesiredState string
			Spec         json.RawMessage
			Status       struct{ State string }
			Scope        string
		}
		if json.Unmarshal(raw, &items) != nil {
			return nil, nil, nil, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
		}
		for _, item := range items {
			switch kind {
			case "services":
				normalized, normalizeErr := canonicalSwarmJSON(item.Spec)
				if normalizeErr != nil {
					return nil, nil, nil, errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
				}
				services[item.ID] = string(normalized)
			case "tasks":
				if item.DesiredState == "running" && item.Status.State == "running" {
					running[item.ServiceID]++
				}
			case "networks":
				if item.Scope == "swarm" {
					networks[item.ID] = true
				}
			}
		}
	}
	return services, running, networks, nil
}

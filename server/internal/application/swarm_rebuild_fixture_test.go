package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type swarmRebuildFixture struct {
	active                                                              bool
	local                                                               swarmLocalConnection
	cluster, node                                                       domain.Object
	networks, services                                                  []domain.Object
	manifest                                                            []byte
	checkpoint                                                          domain.Object
	commands, calls, phases, logs                                       []string
	backupCreated, dataCopied                                           bool
	initCount, leaveCount, serviceCount                                 int
	failBackup, failInitOnce, failAllInit, failServiceOnce, failCleanup bool
	cancelOnLeave                                                       context.CancelFunc
	beforeBackup                                                        func()
}

// Engine 从默认池的第一个 /24 分配 ingress 网关。
func fixtureIngressIPAM() domain.Object {
	pool := netip.MustParsePrefix("10.0.0.0/8")
	subnet := netip.PrefixFrom(pool.Addr(), 24)
	return domain.Object{"Driver": "default", "Config": []domain.Object{{"Subnet": subnet.String(), "Gateway": subnet.Addr().Next().String()}}}
}

func newSwarmRebuildFixture() *swarmRebuildFixture {
	f := &swarmRebuildFixture{active: true, local: swarmLocalConnection{ListenAddr: "0.0.0.0:3377", AdvertiseAddr: netip.AddrPortFrom(fixtureManagerIP, 3377).String()}}
	f.cluster = domain.Object{"ID": "cluster_old", "Version": domain.Object{"Index": 1}, "Spec": domain.Object{"Name": "default", "Labels": nil, "EncryptionConfig": domain.Object{"AutoLockManagers": false}, "CAConfig": domain.Object{"ForceRotate": 0}}, "DataPathPort": 7777, "DefaultAddrPool": nil, "SubnetSize": 24}
	f.node = domain.Object{"ID": "node_old", "Version": domain.Object{"Index": 1}, "Spec": domain.Object{"Role": "manager", "Availability": "active", "Labels": domain.Object{"purpose": "example"}}}
	f.networks = []domain.Object{
		{"Id": "network_ingress", "Name": "ingress", "Driver": "overlay", "Scope": "swarm", "Ingress": true, "Attachable": false, "Internal": false, "EnableIPv6": false, "Labels": nil, "Options": domain.Object{"com.docker.network.driver.overlay.vxlanid_list": "4096"}, "IPAM": fixtureIngressIPAM()},
		{"Id": "network_public", "Name": "iquops-public", "Driver": "overlay", "Scope": "swarm", "Ingress": false, "Attachable": true, "Internal": false, "EnableIPv6": false, "Labels": nil, "Options": domain.Object{"com.docker.network.driver.overlay.vxlanid_list": "4097"}, "IPAM": domain.Object{"Driver": "default", "Config": []domain.Object{{"Subnet": "198.51.100.0/24", "Gateway": "198.51.100.1"}}}},
	}
	f.networks[0]["Containers"] = domain.Object{"ingress-sbox": domain.Object{"Name": "ingress-endpoint"}}
	f.networks[1]["Containers"] = domain.Object{"lb-iquops-public": domain.Object{"Name": "iquops-public-endpoint"}}
	f.services = []domain.Object{{"ID": "service_old", "Spec": domain.Object{"Name": "iquops-traefik", "Labels": domain.Object{"iquops.owner": "iquops", "iquops.component": "traefik_gateway", "iquops.lifecycle": "managed"}, "Mode": domain.Object{"Replicated": domain.Object{"Replicas": 1}}, "TaskTemplate": domain.Object{"ContainerSpec": domain.Object{"Image": "traefik:example@sha256:" + strings.Repeat("a", 64), "Args": []string{"--providers.file.directory=" + traefikRemoteRoot + "/dynamic", "--certificatesresolvers.iquops-le.acme.storage=" + traefikRemoteRoot + "/acme/acme.json"}, "Mounts": []domain.Object{{"Type": "bind", "Source": "/var/run/docker.sock", "Target": "/var/run/docker.sock", "ReadOnly": true}, {"Type": "bind", "Source": traefikRemoteRoot + "/acme", "Target": traefikRemoteRoot + "/acme"}, {"Type": "bind", "Source": traefikRemoteRoot + "/dynamic", "Target": traefikRemoteRoot + "/dynamic"}, {"Type": "bind", "Source": traefikRemoteRoot + "/certificates", "Target": "/certificates"}}}, "Placement": domain.Object{"Constraints": []string{"node.role==manager", "node.id==node_old"}}, "Networks": []domain.Object{{"Target": "network_public"}}}, "EndpointSpec": domain.Object{"Ports": []domain.Object{{"Protocol": "tcp", "TargetPort": 443, "PublishedPort": 443, "PublishMode": "host"}}}}}}
	return f
}

func (f *swarmRebuildFixture) input() ServerRuntimeExecutionInput {
	return withServerRuntimeLogStep(ServerRuntimeExecutionInput{SwarmRebuildID: "task_example", SwarmConnectionMode: "public", SwarmAdvertiseAddr: "192.0.2.10", TraefikService: "iquops-traefik", LogStep: func(_, _, _, message string) { f.logs = append(f.logs, message) }, SaveSwarmRebuildCheckpoint: func(_ context.Context, object domain.Object) error {
		if err := validateSwarmRebuildCheckpointObject(object); err != nil {
			return err
		}
		f.checkpoint = cloneObject(object)
		f.phases = append(f.phases, stringValue(object["phase"]))
		return nil
	}})
}

func (f *swarmRebuildFixture) remote() swarmRebuildRemote {
	return swarmRebuildRemote{Call: f.call, Status: func(ctx context.Context) (remoteSwarmStatus, error) {
		if err := ctx.Err(); err != nil {
			return remoteSwarmStatus{}, err
		}
		if !f.active {
			return remoteSwarmStatus{Status: "inactive"}, nil
		}
		return remoteSwarmStatus{Active: true, Status: "active", ControlAvailable: true, Role: "manager", NodeID: stringValue(f.node["ID"]), ClusterID: stringValue(f.cluster["ID"]), ManagerAddress: f.local.AdvertiseAddr, AdvertiseAddr: strings.Split(f.local.AdvertiseAddr, ":")[0]}, nil
	}, Local: func(context.Context) (swarmLocalConnection, error) { return f.local, nil }, Output: f.output, Write: func(_ context.Context, _ string, raw []byte) error {
		f.manifest = append([]byte(nil), raw...)
		return nil
	}}
}

func (f *swarmRebuildFixture) output(ctx context.Context, command string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.commands = append(f.commands, command)
	switch {
	case strings.Contains(command, "mkdir -m 0700"):
		if f.backupCreated {
			return "", errors.New("already exists")
		}
		f.backupCreated = true
	case strings.Contains(command, "cat --"):
		if len(f.manifest) == 0 {
			return "", errors.New("not found")
		}
		return string(f.manifest), nil
	case strings.Contains(command, "tar -C /opt/iquops -cpf"):
		if len(f.services) != 0 {
			return "", errors.New("gateway still writes")
		}
		if f.beforeBackup != nil {
			f.beforeBackup()
		}
		if f.failBackup {
			return "", errors.New("private remote diagnostic")
		}
		f.dataCopied = true
		return strings.Repeat("b", 64), nil
	case strings.Contains(command, "tar -tf") || strings.Contains(command, "tar -C /opt/iquops -xpf"):
		if !f.dataCopied {
			return "", errors.New("backup missing")
		}
		if strings.Contains(command, "-xpf") && len(f.services) != 0 {
			return "", errors.New("gateway already writes")
		}
	}
	return "", nil
}

func (f *swarmRebuildFixture) call(ctx context.Context, method, requestPath string, body any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.calls = append(f.calls, method+" "+requestPath)
	var value any
	if method == http.MethodGet {
		switch {
		case requestPath == "/swarm":
			if !f.active {
				return nil, errors.New("inactive")
			}
			value = f.cluster
		case requestPath == "/nodes":
			value = []domain.Object{f.nodeWithStatus()}
		case strings.HasPrefix(requestPath, "/nodes/"):
			value = f.nodeWithStatus()
		case requestPath == "/secrets" || requestPath == "/configs":
			value = []domain.Object{}
		case strings.HasPrefix(requestPath, "/images/"):
			value = domain.Object{"Id": "sha256:" + strings.Repeat("a", 64)}
		case requestPath == "/services":
			value = f.services
		case strings.HasPrefix(requestPath, "/tasks?"):
			if len(f.services) > 0 {
				value = []domain.Object{{"DesiredState": "running", "Status": domain.Object{"State": "running"}}}
			} else {
				value = []domain.Object{}
			}
		case strings.HasPrefix(requestPath, "/networks?"):
			value = f.networks
		case strings.HasPrefix(requestPath, "/networks/"):
			id := strings.Split(strings.TrimPrefix(requestPath, "/networks/"), "?")[0]
			for _, network := range f.networks {
				if network["Id"] == id {
					value = network
				}
			}
			if value == nil {
				return nil, errors.New("not found")
			}
		case strings.HasPrefix(requestPath, "/containers/json"):
			value = []domain.Object{}
		default:
			return nil, fmt.Errorf("unexpected GET %s", requestPath)
		}
	} else {
		switch {
		case method == http.MethodDelete && strings.HasPrefix(requestPath, "/services/"):
			f.services = nil
		case requestPath == "/swarm/leave?force=1":
			f.leaveCount++
			f.active = false
			f.networks = nil
			f.services = nil
			if f.cancelOnLeave != nil {
				f.cancelOnLeave()
				f.cancelOnLeave = nil
			}
		case requestPath == "/swarm/init":
			if f.failAllInit {
				return nil, errors.New("private init failure")
			}
			input := rebuildObject(mustRebuildJSON(body))
			f.initCount++
			f.active = true
			f.local = swarmLocalConnection{ListenAddr: stringValue(input["ListenAddr"]), AdvertiseAddr: stringValue(input["AdvertiseAddr"]), DataPathAddr: stringValue(input["DataPathAddr"])}
			f.cluster = domain.Object{"ID": fmt.Sprintf("cluster_new_%d", f.initCount), "Version": domain.Object{"Index": 1}, "Spec": input["Spec"], "DataPathPort": input["DataPathPort"], "SubnetSize": input["SubnetSize"], "DefaultAddrPool": input["DefaultAddrPool"]}
			f.node = domain.Object{"ID": fmt.Sprintf("node_new_%d", f.initCount), "Version": domain.Object{"Index": 1}, "Spec": domain.Object{"Role": "manager", "Availability": "active", "Labels": nil}}
			f.networks = []domain.Object{{"Id": "auto_ingress", "Name": "ingress", "Driver": "overlay", "Scope": "swarm", "Ingress": true, "Attachable": false, "Internal": false, "EnableIPv6": false, "IPAM": fixtureIngressIPAM()}}
			f.networks[0]["Containers"] = domain.Object{"ingress-sbox": domain.Object{"Name": "ingress-endpoint"}}
			if f.failInitOnce {
				f.failInitOnce = false
				return nil, errors.New("response lost after init")
			}
			value = f.node["ID"]
		case strings.HasPrefix(requestPath, "/nodes/") && strings.Contains(requestPath, "/update?"):
			f.node["Spec"] = rebuildObject(mustRebuildJSON(body))
		case method == http.MethodDelete && strings.HasPrefix(requestPath, "/networks/"):
			id := strings.TrimPrefix(requestPath, "/networks/")
			next := []domain.Object{}
			for _, network := range f.networks {
				if network["Id"] != id {
					next = append(next, network)
				}
			}
			f.networks = next
		case requestPath == "/networks/create":
			network := rebuildObject(mustRebuildJSON(body))
			id := fmt.Sprintf("network_new_%d_%d", f.initCount, len(f.networks))
			network["Id"] = id
			name := stringValue(network["Name"])
			key := "lb-" + name
			if network["Ingress"] == true {
				key = name + "-sbox"
			}
			network["Containers"] = domain.Object{key: domain.Object{"Name": name + "-endpoint"}}
			f.networks = append(f.networks, network)
			value = domain.Object{"Id": id}
		case requestPath == "/services/create":
			if f.failServiceOnce {
				f.failServiceOnce = false
				return nil, errors.New("private create failure")
			}
			f.serviceCount++
			f.services = []domain.Object{{"ID": fmt.Sprintf("service_new_%d", f.serviceCount), "Spec": rebuildObject(mustRebuildJSON(body))}}
			value = domain.Object{"ID": f.services[0]["ID"]}
		case strings.HasPrefix(requestPath, "/swarm/update?"):
			if f.failCleanup {
				return nil, errors.New("private cleanup failure")
			}
			f.cluster["Spec"] = rebuildObject(mustRebuildJSON(body))
		default:
			return nil, fmt.Errorf("unexpected mutation %s %s", method, requestPath)
		}
	}
	return json.Marshal(value)
}

func (f *swarmRebuildFixture) nodeWithStatus() domain.Object {
	node := cloneObject(f.node)
	node["ManagerStatus"] = domain.Object{"Addr": f.local.AdvertiseAddr, "Leader": true, "Reachability": "reachable"}
	return node
}

func mustRebuildJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func rebuildAssertNoSecrets(t *testing.T, f *swarmRebuildFixture) {
	t.Helper()
	raw, _ := json.Marshal(f.checkpoint)
	for _, value := range []string{"Mounts", "ContainerSpec", "Labels", "private", "@sha256", "/opt/iquops/traefik"} {
		if strings.Contains(string(raw), value) {
			t.Fatalf("检查点包含完整配置或秘密字段：%s", value)
		}
	}
	for _, message := range f.logs {
		if strings.Contains(message, "private") {
			t.Fatal("原始远端输出进入日志")
		}
	}
}

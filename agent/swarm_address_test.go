package main

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

func TestSwarmManagerCollectsRealAdvertisedAddressesBehindNAT(t *testing.T) {
	local := netip.MustParsePrefix("10.0.0.0/8").Addr().Next()
	snapshot := collectSwarmWithRunner(func(_ string, args ...string) (string, error) {
		switch {
		case args[0] == "info":
			return fmt.Sprintf(`{"NodeID":"manager_node","NodeAddr":%q,"LocalNodeState":"active","ControlAvailable":true,"Cluster":{"ID":"example_cluster"}}`, local.String()), nil
		case strings.Join(args, " ") == "node ls --format {{json .}}":
			return "{\"ID\":\"manager_node\",\"Hostname\":\"gateway-example\",\"ManagerStatus\":\"Leader\"}\n{\"ID\":\"worker_node\",\"Hostname\":\"worker-example\"}", nil
		case strings.Join(args[:2], " ") == "node inspect":
			return fmt.Sprintf(`[{"ID":"manager_node","Spec":{"Labels":{"iquops.server-id":"server_manager"}},"Status":{"Addr":%q},"ManagerStatus":{"Addr":"192.0.2.10:3377"}},{"ID":"worker_node","Spec":{"Labels":{"iquops.server-id":"server_worker"}},"Status":{"Addr":"192.0.2.11"}}]`, local.String()), nil
		case args[0] == "service", args[0] == "network":
			return "", nil
		default:
			return "", errors.New("unexpected command")
		}
	})
	if snapshot.Status != "success" || len(snapshot.Nodes) != 2 {
		t.Fatal("无法采集 Swarm 节点")
	}
	manager, worker := snapshot.Nodes[0], snapshot.Nodes[1]
	if manager.AdvertiseAddr != "192.0.2.10" || manager.ManagerAddress != "192.0.2.10:3377" || manager.ConnectionMode != "public" {
		t.Fatal("manager 实际公网通告丢失")
	}
	if worker.AdvertiseAddr != "192.0.2.11" || worker.ConnectionMode != "public" || worker.ServerID != "server_worker" {
		t.Fatal("worker 实际公网通告丢失")
	}
}

func TestSwarmWorkerLocalReportDoesNotInferAdvertiseAddressFromNATLocalIP(t *testing.T) {
	local := netip.MustParsePrefix("10.0.0.0/8").Addr().Next()
	snapshot := collectSwarmWithRunner(func(_ string, _ ...string) (string, error) {
		return fmt.Sprintf(`{"NodeID":"worker_node","NodeAddr":%q,"LocalNodeState":"active","ControlAvailable":false}`, local.String()), nil
	})
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].AdvertiseAddr != "" || snapshot.Nodes[0].ConnectionMode != "" {
		t.Fatal("worker 本机报告伪造了通告地址")
	}
}

package application

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmRebuildRecognizesExactMobyInternalEndpointsAndGateway(t *testing.T) {
	f := newSwarmRebuildFixture()
	gatewayContainerID := strings.Repeat("a", 64)
	objectMapFromAny(f.networks[1]["Containers"])[gatewayContainerID] = domain.Object{"Name": "iquops-traefik.1.example"}
	remote := f.remote()
	remote.Call = func(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
		if method == http.MethodGet && path == "/containers/json?all=1" {
			return json.Marshal([]domain.Object{{"ID": gatewayContainerID, "State": "running", "Labels": domain.Object{"com.docker.swarm.service.id": "service_old"}}})
		}
		return f.call(ctx, method, path, body)
	}
	m, err := readSwarmRebuildManifest(context.Background(), remote.Call, f.input(), f.local)
	if err != nil || len(m.Networks) != 2 {
		t.Fatalf("真实内置sandbox和已核实网关被误判为用户端点：%v", err)
	}
}

func TestSwarmRebuildRejectsSpoofedOrUnknownInternalEndpoints(t *testing.T) {
	for _, test := range []struct {
		name          string
		network       int
		key, endpoint string
		change        func(domain.Object)
	}{
		{"ingress名称不符", 0, "ingress-sbox", "other-endpoint", nil},
		{"ingress伪装用户容器", 0, strings.Repeat("b", 64), "ingress-endpoint", nil},
		{"普通网络不能借用ingress", 1, "ingress-sbox", "iquops-public-endpoint", nil},
		{"任意lb前缀", 1, "lb-unrecognized", "iquops-public-endpoint", nil},
		{"网络ID不是sandbox名", 1, "lb-network_public", "iquops-public-endpoint", nil},
		{"lb端点名称不符", 1, "lb-iquops-public", "standalone-container", nil},
		{"未知网络类型", 1, "lb-iquops-public", "iquops-public-endpoint", func(n domain.Object) { n["Driver"] = "bridge" }},
		{"未知网络范围", 1, "lb-iquops-public", "iquops-public-endpoint", func(n domain.Object) { n["Scope"] = "local" }},
		{"非标准ingress名称", 0, "custom-sbox", "custom-endpoint", func(n domain.Object) { n["Name"] = "custom" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSwarmRebuildFixture()
			network := f.networks[test.network]
			network["Containers"] = domain.Object{test.key: domain.Object{"Name": test.endpoint}}
			if test.change != nil {
				test.change(network)
			}
			m := swarmRebuildManifest{GatewayName: "iquops-traefik", Networks: []json.RawMessage{mustRebuildJSON(network)}}
			err := validateSwarmRebuildContainers(context.Background(), f.call, m)
			if err == nil || err.Error() != "SWARM_REBUILD_SCOPE_UNSUPPORTED" || swarmRebuildReason(err) != "NETWORK_ENDPOINT" {
				t.Fatalf("伪装或未知网络端点被放行：%v", err)
			}
		})
	}
}

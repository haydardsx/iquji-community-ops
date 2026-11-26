package application

import (
	"reflect"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestReleasePlanRuntimeConfigKeepsSnapshotBuildServerAndTargets(t *testing.T) {
	releasePlan := domain.Object{
		"id": "rel_1", "applicationId": "app_1", "buildServerId": "srv_build",
		"deploymentManagerServerId": "srv_gateway",
		"buildSnapshot": domain.Object{
			"buildServerId": "srv_build",
			"swarmRuntime": domain.Object{"services": []domain.Object{
				{"serverId": "srv_worker_b"},
				{"serverId": "srv_worker_a"},
			}},
		},
	}

	config := releasePlanRuntimeConfig(releasePlan)
	if config["buildServerId"] != "srv_build" || config["deploymentManagerServerId"] != "srv_gateway" {
		t.Fatal("release snapshot target changed")
	}
	expected := []string{"srv_build", "srv_gateway", "srv_worker_a", "srv_worker_b"}
	if actual := releaseRuntimeServerIDs(config); !reflect.DeepEqual(actual, expected) {
		t.Fatal("runtime server IDs differ from the expected fixture")
	}
}

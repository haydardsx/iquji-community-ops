package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestProcessRepositoryPersistsRunChecksAndResourceGraph(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "iquops.db"), "../../../../")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	server := domain.Resource("server", "srv_1", "gateway")
	application := domain.Resource("application", "app_1", "api")
	report := domain.NewCheckReportBuilder(domain.CheckKindRuntimeProbe, server).
		Check("SSH_OK", "runtime", "probe", server, domain.CheckOutcomePassed, domain.CheckSeverityInfo, true, "SSH reachable", nil).
		Impact(domain.ResourceActionInspect, server, 1, domain.CheckSeverityInfo, "SERVER_RUNTIME_PROBE").
		Report()

	if err := store.CreateProcessRun(ctx, report); err != nil {
		t.Fatalf("create process run: %v", err)
	}
	if err := store.UpsertResourceLink(ctx, application, server, "runs_on", domain.Object{"source": "test"}); err != nil {
		t.Fatalf("upsert resource link: %v", err)
	}

	graph, err := store.ListResourceGraph(ctx, server)
	if err != nil {
		t.Fatalf("list graph: %v", err)
	}
	if len(graph) != 1 {
		t.Fatalf("graph links = %d, want 1", len(graph))
	}
	if graph[0].Source.Kind != "application" || graph[0].Target.Kind != "server" || graph[0].Relation != "runs_on" {
		t.Fatalf("unexpected graph link: %#v", graph[0])
	}
}

package sqlite

import (
	"context"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestMutateStateWithSecretChangesAndRebuildNormalizedHistoryCommitsTogether(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTestStore(t, ctx)
	defer func() {
		_ = store.Close()
	}()

	if err := store.UpsertSecretMaterial(ctx, "secret_old", "old-value"); err != nil {
		t.Fatalf("seed old secret: %v", err)
	}
	seedOldNormalizedHistory(t, ctx, store)

	err := store.MutateStateWithSecretChangesAndRebuildNormalizedHistory(
		ctx,
		map[string]string{"secret_new": "new-value"},
		[]string{"secret_old"},
		func(state *domain.State) error {
			state.Servers = []domain.Object{{"id": "srv_new", "name": "New server"}}
			state.MetricSamples = []domain.Object{{
				"id":         "metric_new",
				"serverId":   "srv_new",
				"targetKind": "server",
				"targetId":   "srv_new",
				"projectId":  "project_new",
				"sampledAt":  "2026-07-14T01:00:00Z",
				"values":     domain.Object{"cpuPercent": 42.5},
				"labels":     domain.Object{"source": "restore"},
			}}
			state.MetricAggregates = []domain.Object{{
				"id":          "aggregate_new",
				"serverId":    "srv_new",
				"targetKind":  "server",
				"targetId":    "srv_new",
				"window":      "1h",
				"bucketStart": "2026-07-14T01:00:00Z",
				"values":      domain.Object{"cpuPercent": 40},
				"labels":      domain.Object{"source": "restore"},
				"sampleCount": 3,
				"createdAt":   "2026-07-14T01:00:00Z",
				"updatedAt":   "2026-07-14T01:00:00Z",
			}}
			state.SwarmSnapshots = []domain.Object{{
				"id":        "swarm_new",
				"serverId":  "srv_new",
				"sampledAt": "2026-07-14T01:00:00Z",
				"nodes":     []domain.Object{{"id": "node_1"}},
				"services":  []domain.Object{{"id": "service_1"}},
				"tasks":     []domain.Object{{"id": "task_1"}},
				"networks":  []domain.Object{{"id": "network_1"}},
				"summary":   "restored",
				"status":    "success",
			}}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("mutate and rebuild normalized history: %v", err)
	}

	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(state.Servers) != 1 || domain.StringValue(state.Servers[0]["id"]) != "srv_new" || len(state.MetricSamples) != 1 {
		t.Fatal("state mutation did not commit")
	}
	if _, err := store.GetSecretMaterial(ctx, "secret_old"); err == nil {
		t.Fatal("deleted secret material remains")
	}
	if value, err := store.GetSecretMaterial(ctx, "secret_new"); err != nil || value != "new-value" {
		t.Fatal("new secret material mismatch")
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM metric_samples WHERE id = 'metric_old'"); count != 0 {
		t.Fatalf("old metric sample count = %d, want 0", count)
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM metric_samples WHERE id = 'metric_new'"); count != 1 {
		t.Fatalf("new metric sample count = %d, want 1", count)
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM metric_aggregates WHERE id = 'aggregate_new' AND sample_count = 3"); count != 1 {
		t.Fatalf("normalized metric aggregate was not rebuilt")
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM swarm_snapshots WHERE id = 'swarm_new' AND summary = 'restored'"); count != 1 {
		t.Fatalf("normalized swarm snapshot was not rebuilt")
	}
	var values, labels string
	if err := store.db.QueryRowContext(ctx, "SELECT \"values\", labels FROM metric_samples WHERE id = 'metric_new'").Scan(&values, &labels); err != nil {
		t.Fatalf("read normalized metric payload: %v", err)
	}
	if values != `{"cpuPercent":42.5}` || labels != `{"source":"restore"}` {
		t.Fatal("normalized metric payload mismatch")
	}
}

func TestMutateStateWithSecretChangesKeepsLegacyNormalizedHistoryBehavior(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTestStore(t, ctx)
	defer func() {
		_ = store.Close()
	}()
	seedOldNormalizedHistory(t, ctx, store)

	if err := store.MutateStateWithSecretChanges(ctx, nil, nil, func(state *domain.State) error {
		state.MetricSamples = []domain.Object{{
			"id":         "metric_state_only",
			"serverId":   "srv_state",
			"targetKind": "server",
			"targetId":   "srv_state",
			"sampledAt":  "2026-07-14T02:00:00Z",
		}}
		return nil
	}); err != nil {
		t.Fatalf("legacy mutation API: %v", err)
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM metric_samples WHERE id = 'metric_old'"); count != 1 {
		t.Fatalf("legacy API unexpectedly rebuilt normalized history")
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM metric_samples WHERE id = 'metric_state_only'"); count != 0 {
		t.Fatalf("legacy API unexpectedly inserted state-only history")
	}
}

func TestMutateStateWithSecretChangesAndRebuildNormalizedHistoryRollsBackEverything(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTestStore(t, ctx)
	defer func() {
		_ = store.Close()
	}()

	initial := domain.EmptyState()
	initial.Servers = []domain.Object{{"id": "srv_old"}}
	initial.MetricSamples = []domain.Object{{
		"id": "metric_old", "serverId": "srv_old", "targetKind": "server", "targetId": "srv_old", "sampledAt": "2026-07-14T00:00:00Z",
	}}
	initial.MetricAggregates = []domain.Object{{
		"id": "aggregate_old", "serverId": "srv_old", "targetKind": "server", "targetId": "srv_old", "window": "1h", "bucketStart": "2026-07-14T00:00:00Z",
	}}
	initial.SwarmSnapshots = []domain.Object{{
		"id": "swarm_old", "serverId": "srv_old", "sampledAt": "2026-07-14T00:00:00Z", "summary": "old", "status": "success",
	}}
	if err := store.ReplaceState(ctx, initial); err != nil {
		t.Fatalf("replace initial state: %v", err)
	}
	if err := store.UpsertSecretMaterial(ctx, "secret_old", "old-value"); err != nil {
		t.Fatalf("seed old secret: %v", err)
	}
	seedOldNormalizedHistory(t, ctx, store)
	maintenanceExec(t, ctx, store, `CREATE TRIGGER fail_restored_swarm_insert
		BEFORE INSERT ON swarm_snapshots
		WHEN NEW.id = 'swarm_fail'
		BEGIN
			SELECT RAISE(ABORT, 'forced normalized history failure');
		END`)

	err := store.MutateStateWithSecretChangesAndRebuildNormalizedHistory(
		ctx,
		map[string]string{"secret_new": "new-value"},
		[]string{"secret_old"},
		func(state *domain.State) error {
			state.Servers = []domain.Object{{"id": "srv_new"}}
			state.MetricSamples = []domain.Object{{
				"id": "metric_new", "serverId": "srv_new", "targetKind": "server", "targetId": "srv_new", "sampledAt": "2026-07-14T03:00:00Z",
			}}
			state.MetricAggregates = []domain.Object{{
				"id": "aggregate_new", "serverId": "srv_new", "targetKind": "server", "targetId": "srv_new", "window": "1h", "bucketStart": "2026-07-14T03:00:00Z",
			}}
			state.SwarmSnapshots = []domain.Object{{
				"id": "swarm_fail", "serverId": "srv_new", "sampledAt": "2026-07-14T03:00:00Z", "summary": "fail", "status": "danger",
			}}
			return nil
		},
	)
	if err == nil {
		t.Fatal("normalized history failure should roll back the transaction")
	}

	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load rolled back state: %v", err)
	}
	if len(state.Servers) != 1 || domain.StringValue(state.Servers[0]["id"]) != "srv_old" || domain.StringValue(state.MetricSamples[0]["id"]) != "metric_old" {
		t.Fatal("app_state did not roll back")
	}
	if value, err := store.GetSecretMaterial(ctx, "secret_old"); err != nil || value != "old-value" {
		t.Fatal("deleted secret did not roll back")
	}
	if _, err := store.GetSecretMaterial(ctx, "secret_new"); err == nil {
		t.Fatal("inserted secret did not roll back")
	}
	for table, id := range map[string]string{
		"metric_samples":    "metric_old",
		"metric_aggregates": "aggregate_old",
		"swarm_snapshots":   "swarm_old",
	} {
		query := "SELECT COUNT(*) FROM " + quoteSQLiteIdentifier(table) + " WHERE id = ?"
		var count int
		if err := store.db.QueryRowContext(ctx, query, id).Scan(&count); err != nil {
			t.Fatalf("query rolled back %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s old row count = %d, want 1", table, count)
		}
	}
}

func seedOldNormalizedHistory(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	maintenanceExec(t, ctx, store, `INSERT INTO metric_samples
		(id, server_id, target_kind, target_id, sampled_at, "values", labels)
		VALUES ('metric_old', 'srv_old', 'server', 'srv_old', '2026-07-14T00:00:00Z', '{}', '{}')`)
	maintenanceExec(t, ctx, store, `INSERT INTO metric_aggregates
		(id, server_id, target_kind, target_id, "window", bucket_start, "values", labels, sample_count)
		VALUES ('aggregate_old', 'srv_old', 'server', 'srv_old', '1h', '2026-07-14T00:00:00Z', '{}', '{}', 1)`)
	maintenanceExec(t, ctx, store, `INSERT INTO swarm_snapshots
		(id, server_id, sampled_at, nodes, services, tasks, networks, summary, status)
		VALUES ('swarm_old', 'srv_old', '2026-07-14T00:00:00Z', '[]', '[]', '[]', '[]', 'old', 'success')`)
}

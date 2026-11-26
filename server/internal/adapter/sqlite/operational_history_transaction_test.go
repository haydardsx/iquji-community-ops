package sqlite

import (
	"context"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestRestoreOperationalHistoryCommitsAndPreservesUnimportedRows(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTestStore(t, ctx)
	defer func() {
		_ = store.Close()
	}()

	initial := normalizedHistoryState("old", "srv_old")
	if err := store.ReplaceState(ctx, initial); err != nil {
		t.Fatalf("replace initial state: %v", err)
	}
	seedOldNormalizedHistory(t, ctx, store)
	if err := store.CreateProcessRun(ctx, terminalCheckReport("process_existing", domain.CheckStatusPassed, "existing")); err != nil {
		t.Fatalf("seed existing process report: %v", err)
	}
	if _, err := store.RecordFrpProbeHit(ctx, restoredProbeHit("hit_existing", "tunnel_existing", "run_existing", "/existing"), DefaultFrpProbeMaxHits); err != nil {
		t.Fatalf("seed existing probe hit: %v", err)
	}

	err := store.MutateStateWithSecretChangesAndRestoreOperationalHistory(
		ctx,
		map[string]string{"secret_restored": "restored-value"},
		nil,
		[]domain.CheckReport{terminalCheckReport("process_restored", domain.CheckStatusWarning, "restored")},
		[]domain.FrpProbeHit{restoredProbeHit("hit_restored", "tunnel_restored", "run_restored", "/restored")},
		func(state *domain.State) error {
			state.Servers = append(state.Servers, domain.Object{"id": "srv_restored"})
			appendNormalizedHistory(state, "restored", "srv_restored")
			return nil
		},
	)
	if err != nil {
		t.Fatalf("restore operational history: %v", err)
	}

	reports, err := store.ListCheckReports(ctx)
	if err != nil {
		t.Fatalf("list process reports: %v", err)
	}
	if len(reports) != 2 || !hasCheckReport(reports, "process_existing", "existing") || !hasCheckReport(reports, "process_restored", "restored") {
		t.Fatal("process reports were not preserved and appended")
	}
	hits, err := store.ListFrpProbeHitsForTunnels(ctx, []string{"tunnel_restored", "tunnel_existing", "tunnel_restored", ""})
	if err != nil {
		t.Fatalf("list probe hits: %v", err)
	}
	if len(hits) != 2 || !hasProbeHit(hits, "hit_existing", "/existing") || !hasProbeHit(hits, "hit_restored", "/restored") {
		t.Fatal("probe hits were not preserved and appended")
	}
	if empty, err := store.ListFrpProbeHitsForTunnels(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatal("empty tunnel filter returned unexpected probe hits")
	}
	for _, tunnelID := range []string{"tunnel_existing", "tunnel_restored"} {
		if count, err := store.FrpProbeHitCount(ctx, tunnelID); err != nil || count != 1 {
			t.Fatalf("probe stats for %s = %d err=%v, want 1", tunnelID, count, err)
		}
	}
	for table, expected := range map[string]int{
		"metric_samples":    2,
		"metric_aggregates": 2,
		"swarm_snapshots":   2,
	} {
		if count := maintenanceTableCount(t, ctx, store, table); count != expected {
			t.Fatalf("%s count = %d, want %d", table, count, expected)
		}
	}
	if value, err := store.GetSecretMaterial(ctx, "secret_restored"); err != nil || value != "restored-value" {
		t.Fatal("restored secret mismatch")
	}
}

func TestRestoreOperationalHistoryUpsertsIDsAndRecalculatesProbeStats(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTestStore(t, ctx)
	defer func() {
		_ = store.Close()
	}()

	if err := store.CreateProcessRun(ctx, terminalCheckReport("process_same", domain.CheckStatusPassed, "old")); err != nil {
		t.Fatalf("seed same process report: %v", err)
	}
	if err := store.CreateProcessRun(ctx, terminalCheckReport("process_unrelated", domain.CheckStatusPassed, "unrelated")); err != nil {
		t.Fatalf("seed unrelated process report: %v", err)
	}
	if _, err := store.RecordFrpProbeHit(ctx, restoredProbeHit("hit_same", "tunnel_old", "run_old", "/old"), DefaultFrpProbeMaxHits); err != nil {
		t.Fatalf("seed same probe hit: %v", err)
	}
	if _, err := store.RecordFrpProbeHit(ctx, restoredProbeHit("hit_unrelated", "tunnel_unrelated", "run_unrelated", "/unrelated"), DefaultFrpProbeMaxHits); err != nil {
		t.Fatalf("seed unrelated probe hit: %v", err)
	}

	replacementReport := terminalCheckReport("process_same", domain.CheckStatusFailed, "replacement")
	replacementReport.Checks = []domain.CheckItem{{
		ID:         "check_replacement",
		Code:       "RESTORED",
		Category:   "restore",
		Phase:      "complete",
		Target:     replacementReport.Subject,
		Outcome:    domain.CheckOutcomeFailed,
		Severity:   domain.CheckSeverityDanger,
		Blocking:   true,
		Summary:    "replacement",
		Details:    domain.Object{},
		ObservedAt: "2026-07-14T02:00:00Z",
	}}
	replacementHit := restoredProbeHit("hit_same", "tunnel_new", "run_new", "/new")
	if err := store.MutateStateWithSecretChangesAndRestoreOperationalHistory(
		ctx,
		nil,
		nil,
		[]domain.CheckReport{replacementReport},
		[]domain.FrpProbeHit{replacementHit},
		func(*domain.State) error { return nil },
	); err != nil {
		t.Fatalf("upsert operational history: %v", err)
	}

	reports, err := store.ListCheckReports(ctx)
	if err != nil {
		t.Fatalf("list reports after upsert: %v", err)
	}
	if len(reports) != 2 || !hasCheckReport(reports, "process_same", "replacement") || !hasCheckReport(reports, "process_unrelated", "unrelated") {
		t.Fatal("process ID upsert affected unrelated rows")
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM process_checks WHERE process_id = 'process_same' AND id = 'check_replacement'"); count != 1 {
		t.Fatalf("replacement process checks were not stored")
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM process_checks WHERE process_id = 'process_same' AND id = 'check_process_same'"); count != 0 {
		t.Fatalf("old process checks were not replaced")
	}
	hits, err := store.ListFrpProbeHitsForTunnels(ctx, []string{"tunnel_old", "tunnel_new", "tunnel_unrelated"})
	if err != nil {
		t.Fatalf("list hits after upsert: %v", err)
	}
	if len(hits) != 2 || !hasProbeHit(hits, "hit_same", "/new") || !hasProbeHit(hits, "hit_unrelated", "/unrelated") {
		t.Fatal("probe ID upsert affected unrelated rows")
	}
	for tunnelID, expected := range map[string]int{
		"tunnel_old":       0,
		"tunnel_new":       1,
		"tunnel_unrelated": 1,
	} {
		if count, err := store.FrpProbeHitCount(ctx, tunnelID); err != nil || count != expected {
			t.Fatalf("probe stats for %s = %d err=%v, want %d", tunnelID, count, err, expected)
		}
	}
}

func TestRestoreOperationalHistoryRollsBackAllPersistenceOnFailure(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTestStore(t, ctx)
	defer func() {
		_ = store.Close()
	}()

	initial := normalizedHistoryState("old", "srv_old")
	initial.Servers = []domain.Object{{"id": "srv_old"}}
	if err := store.ReplaceState(ctx, initial); err != nil {
		t.Fatalf("replace initial state: %v", err)
	}
	seedOldNormalizedHistory(t, ctx, store)
	if err := store.UpsertSecretMaterial(ctx, "secret_old", "old-value"); err != nil {
		t.Fatalf("seed old secret: %v", err)
	}
	if err := store.CreateProcessRun(ctx, terminalCheckReport("process_existing", domain.CheckStatusPassed, "existing")); err != nil {
		t.Fatalf("seed process report: %v", err)
	}
	if _, err := store.RecordFrpProbeHit(ctx, restoredProbeHit("hit_existing", "tunnel_existing", "run_existing", "/existing"), DefaultFrpProbeMaxHits); err != nil {
		t.Fatalf("seed probe hit: %v", err)
	}
	maintenanceExec(t, ctx, store, `CREATE TRIGGER fail_operational_history_probe_insert
		BEFORE INSERT ON frp_probe_hits
		WHEN NEW.id = 'hit_fail'
		BEGIN
			SELECT RAISE(ABORT, 'forced operational history failure');
		END`)

	err := store.MutateStateWithSecretChangesAndRestoreOperationalHistory(
		ctx,
		map[string]string{"secret_new": "new-value"},
		[]string{"secret_old"},
		[]domain.CheckReport{terminalCheckReport("process_new", domain.CheckStatusFailed, "new")},
		[]domain.FrpProbeHit{restoredProbeHit("hit_fail", "tunnel_fail", "run_fail", "/fail")},
		func(state *domain.State) error {
			state.Servers = []domain.Object{{"id": "srv_new"}}
			state.MetricSamples = nil
			state.MetricAggregates = nil
			state.SwarmSnapshots = nil
			appendNormalizedHistory(state, "new", "srv_new")
			return nil
		},
	)
	if err == nil {
		t.Fatal("operational history failure should roll back")
	}

	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load rolled back state: %v", err)
	}
	if len(state.Servers) != 1 || domain.StringValue(state.Servers[0]["id"]) != "srv_old" || domain.StringValue(state.MetricSamples[0]["id"]) != "metric_old" {
		t.Fatal("app_state did not roll back")
	}
	if value, err := store.GetSecretMaterial(ctx, "secret_old"); err != nil || value != "old-value" {
		t.Fatal("old secret did not roll back")
	}
	if _, err := store.GetSecretMaterial(ctx, "secret_new"); err == nil {
		t.Fatal("new secret survived rolled back transaction")
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM process_runs WHERE id = 'process_new'"); count != 0 {
		t.Fatalf("new process report survived rollback")
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM process_runs WHERE id = 'process_existing'"); count != 1 {
		t.Fatalf("existing process report was lost during rollback")
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM frp_probe_hits WHERE id = 'hit_fail'"); count != 0 {
		t.Fatalf("failed probe hit survived rollback")
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM frp_probe_hits WHERE id = 'hit_existing'"); count != 1 {
		t.Fatalf("existing probe hit was lost during rollback")
	}
	for table, id := range map[string]string{
		"metric_samples":    "metric_old",
		"metric_aggregates": "aggregate_old",
		"swarm_snapshots":   "swarm_old",
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteSQLiteIdentifier(table)+" WHERE id = ?", id).Scan(&count); err != nil {
			t.Fatalf("query rolled back %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s old row count = %d, want 1", table, count)
		}
	}
	if count, err := store.FrpProbeHitCount(ctx, "tunnel_existing"); err != nil || count != 1 {
		t.Fatalf("existing probe stats changed during rollback: count=%d err=%v", count, err)
	}
}

func terminalCheckReport(id string, status string, summary string) domain.CheckReport {
	return domain.CheckReport{
		ID:      id,
		Kind:    domain.CheckKindTask,
		Status:  status,
		Subject: domain.ResourceRef{Kind: "server", ID: "srv_" + id, Name: summary},
		Decision: domain.CheckDecision{
			Allowed:  status != domain.CheckStatusFailed && status != domain.CheckStatusBlocked,
			Blockers: []string{},
			Warnings: []string{},
		},
		Checks: []domain.CheckItem{{
			ID:         "check_" + id,
			Code:       "CHECK_" + id,
			Category:   "test",
			Phase:      "complete",
			Target:     domain.ResourceRef{Kind: "server", ID: "srv_" + id, Name: summary},
			Outcome:    domain.CheckOutcomePassed,
			Severity:   domain.CheckSeverityInfo,
			Summary:    summary,
			Details:    domain.Object{},
			ObservedAt: "2026-07-14T00:00:00Z",
		}},
		Impacts:     []domain.ResourceImpact{},
		Resources:   []domain.ResourceRef{{Kind: "server", ID: "srv_" + id, Name: summary}},
		Metrics:     domain.Object{"summary": summary},
		CompletedAt: "2026-07-14T00:00:00Z",
		GeneratedAt: "2026-07-14T00:00:00Z",
	}
}

func restoredProbeHit(id string, tunnelID string, runID string, path string) domain.FrpProbeHit {
	return domain.FrpProbeHit{
		ID:          id,
		TunnelID:    tunnelID,
		RunID:       runID,
		MappingName: "http",
		Method:      "GET",
		Host:        "example.test",
		Path:        path,
		RemoteAddr:  "127.0.0.1",
		ReceivedAt:  "2026-07-14T00:00:00Z",
	}
}

func normalizedHistoryState(suffix string, serverID string) domain.State {
	state := domain.EmptyState()
	appendNormalizedHistory(&state, suffix, serverID)
	return state
}

func appendNormalizedHistory(state *domain.State, suffix string, serverID string) {
	state.MetricSamples = append(state.MetricSamples, domain.Object{
		"id":         "metric_" + suffix,
		"serverId":   serverID,
		"targetKind": "server",
		"targetId":   serverID,
		"sampledAt":  "2026-07-14T00:00:00Z",
		"values":     domain.Object{"cpuPercent": 1},
		"labels":     domain.Object{},
	})
	state.MetricAggregates = append(state.MetricAggregates, domain.Object{
		"id":          "aggregate_" + suffix,
		"serverId":    serverID,
		"targetKind":  "server",
		"targetId":    serverID,
		"window":      "1h",
		"bucketStart": "2026-07-14T00:00:00Z",
		"values":      domain.Object{"cpuPercent": 1},
		"labels":      domain.Object{},
		"sampleCount": 1,
	})
	state.SwarmSnapshots = append(state.SwarmSnapshots, domain.Object{
		"id":        "swarm_" + suffix,
		"serverId":  serverID,
		"sampledAt": "2026-07-14T00:00:00Z",
		"nodes":     []domain.Object{},
		"services":  []domain.Object{},
		"tasks":     []domain.Object{},
		"networks":  []domain.Object{},
		"summary":   suffix,
		"status":    "success",
	})
}

func hasCheckReport(reports []domain.CheckReport, id string, summary string) bool {
	for _, report := range reports {
		if report.ID == id && domain.StringValue(report.Metrics["summary"]) == summary {
			return true
		}
	}
	return false
}

func hasProbeHit(hits []domain.FrpProbeHit, id string, path string) bool {
	for _, hit := range hits {
		if hit.ID == id && hit.Path == path {
			return true
		}
	}
	return false
}

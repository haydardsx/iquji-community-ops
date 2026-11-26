package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestClearOperationalHistoryPreservesBusinessAndSecrets(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTestStore(t, ctx)
	defer func() {
		_ = store.Close()
	}()

	state := populatedMaintenanceState()
	state.BackupRestoreJobs = append(state.BackupRestoreJobs, domain.Object{
		"id":               "upload_1",
		"kind":             "restore_upload",
		"sessionSecretRef": "secret_restore_session",
	})
	if err := store.ReplaceState(ctx, state); err != nil {
		t.Fatalf("replace state: %v", err)
	}
	if err := store.UpsertSecretMaterial(ctx, "secret_ssh", "private-key"); err != nil {
		t.Fatalf("upsert secret: %v", err)
	}
	if err := store.UpsertSecretMaterial(ctx, "secret_restore_session", "random-session-key"); err != nil {
		t.Fatalf("upsert restore session secret: %v", err)
	}
	seedMaintenanceTables(t, ctx, store)

	if err := store.ClearOperationalHistory(ctx); err != nil {
		t.Fatalf("clear operational history: %v", err)
	}

	got, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load cleared state: %v", err)
	}
	if len(got.Projects) != 2 || len(got.Applications) != 1 || len(got.Servers) != 1 || len(got.SecretAssets) != 1 {
		t.Fatal("business resources were removed")
	}
	if len(got.Domains) != 1 || len(got.Certificates) != 1 || len(got.FrpTunnels) != 1 {
		t.Fatal("retained resource families changed")
	}
	if len(got.ReleasePlans) != 1 || len(got.ApplicationInstances) != 1 || len(got.RuntimeLogStreams) != 1 || len(got.MonitoringAgents) != 1 {
		t.Fatal("retained runtime configuration changed")
	}
	if len(got.Tasks) != 0 || len(got.ConfirmationTokens) != 0 || len(got.RuntimeLogs) != 0 || len(got.OperationLogs) != 0 || len(got.BuildLogs) != 0 || len(got.JenkinsReleaseRuns) != 0 || len(got.BackupRestoreJobs) != 0 {
		t.Fatal("operational history remains in app_state")
	}
	if len(got.HealthSnapshots) != 0 || len(got.MetricSamples) != 0 || len(got.MetricAggregates) != 0 || len(got.SwarmSnapshots) != 0 || len(got.FrpTunnelRuns) != 0 {
		t.Fatal("snapshot or tunnel history remains")
	}
	if _, exists := got.Servers[0]["healthSnapshotId"]; exists {
		t.Fatal("server retained a deleted health snapshot reference")
	}
	if got.MonitoringAgents[0]["status"] != "offline" || got.MonitoringAgents[0]["lastHeartbeatAt"] != nil {
		t.Fatal("monitoring registration was not reset safely")
	}
	if secret, err := store.GetSecretMaterial(ctx, "secret_ssh"); err != nil || secret != "private-key" {
		t.Fatal("secret material was not preserved")
	}
	if _, err := store.GetSecretMaterial(ctx, "secret_restore_session"); err == nil {
		t.Fatal("restore upload session secret was not removed")
	}

	for _, table := range []string{
		"platform_runtime_status",
		"metric_samples",
		"metric_aggregates",
		"swarm_snapshots",
		"process_runs",
		"process_checks",
		"process_resources",
		"frp_probe_hits",
		"frp_probe_stats",
	} {
		if count := maintenanceTableCount(t, ctx, store, table); count != 0 {
			t.Fatalf("%s count = %d, want 0", table, count)
		}
	}
	if count := maintenanceTableCount(t, ctx, store, "monitoring_agents"); count != 1 {
		t.Fatalf("monitoring agent registration count = %d, want 1", count)
	}
	if count := maintenanceTableCount(t, ctx, store, "domains"); count != 1 {
		t.Fatalf("normalized domain count = %d, want 1", count)
	}
	if count := maintenanceTableCount(t, ctx, store, "secret_materials"); count != 1 {
		t.Fatalf("secret material count = %d, want 1", count)
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM resource_inventory WHERE resource_kind = 'stale'"); count != 0 {
		t.Fatalf("stale resource inventory count = %d, want 0", count)
	}
	if count := maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM resource_inventory WHERE resource_kind = 'server' AND resource_id = 'srv_1'"); count != 1 {
		t.Fatalf("retained server was not rebuilt in resource inventory")
	}
	if count := maintenanceRowCount(t, ctx, store, `SELECT COUNT(*) FROM resource_links
		WHERE source_kind = 'project' AND source_id = 'prj_1'
		  AND target_kind = 'server' AND target_id = 'srv_1'`); count != 1 {
		t.Fatalf("retained project-server link was not rebuilt")
	}
	var status string
	var heartbeat, sample, lastError any
	if err := store.db.QueryRowContext(ctx, `SELECT status, last_heartbeat_at, last_sample_at, last_error
		FROM monitoring_agents WHERE id = 'agent_1'`).Scan(&status, &heartbeat, &sample, &lastError); err != nil {
		t.Fatalf("read normalized monitoring agent: %v", err)
	}
	if status != "offline" || heartbeat != nil || sample != nil || lastError != nil {
		t.Fatal("normalized monitoring history was not cleared")
	}
}

func TestFactoryResetClearsEveryMutableTableAndKeepsMigrations(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTestStore(t, ctx)
	defer func() {
		_ = store.Close()
	}()

	if err := store.ReplaceState(ctx, populatedMaintenanceState()); err != nil {
		t.Fatalf("replace state: %v", err)
	}
	if err := store.UpsertSecretMaterial(ctx, "secret_ssh", "private-key"); err != nil {
		t.Fatalf("upsert secret: %v", err)
	}
	seedMaintenanceTables(t, ctx, store)
	migrationCount := maintenanceTableCount(t, ctx, store, "_migrations")

	fresh := domain.EmptyState()
	if err := store.FactoryReset(ctx, fresh); err != nil {
		t.Fatalf("factory reset: %v", err)
	}

	got, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load fresh state: %v", err)
	}
	if len(got.Projects) != 1 || got.Projects[0]["id"] != domain.BuiltinProjectID || len(got.Servers) != 0 || len(got.SecretAssets) != 0 || len(got.Tasks) != 0 || len(got.BackupRestoreJobs) != 0 {
		t.Fatal("user state survived factory reset")
	}
	if count := maintenanceTableCount(t, ctx, store, "_migrations"); count != migrationCount || count == 0 {
		t.Fatalf("migration count = %d, want preserved %d", count, migrationCount)
	}
	if count := maintenanceTableCount(t, ctx, store, "app_state"); count != 1 {
		t.Fatalf("app_state count = %d, want 1", count)
	}
	for _, table := range maintenanceMutableTables(t, ctx, store) {
		if count := maintenanceTableCount(t, ctx, store, table); count != 0 {
			t.Fatalf("mutable table %s count = %d, want 0", table, count)
		}
	}
}

func TestClearOperationalHistoryRollsBackStateAndTables(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTestStore(t, ctx)
	defer func() {
		_ = store.Close()
	}()

	state := populatedMaintenanceState()
	state.BackupRestoreJobs = append(state.BackupRestoreJobs, domain.Object{
		"id":               "upload_rollback",
		"kind":             "restore_upload",
		"sessionSecretRef": "secret_restore_rollback",
	})
	if err := store.ReplaceState(ctx, state); err != nil {
		t.Fatalf("replace state: %v", err)
	}
	if err := store.UpsertSecretMaterial(ctx, "secret_restore_rollback", "rollback-session-key"); err != nil {
		t.Fatalf("upsert restore session secret: %v", err)
	}
	seedMaintenanceTables(t, ctx, store)
	maintenanceExec(t, ctx, store, `CREATE TRIGGER fail_metric_history_delete
		BEFORE DELETE ON metric_samples
		BEGIN
			SELECT RAISE(ABORT, 'forced clear failure');
		END`)

	if err := store.ClearOperationalHistory(ctx); err == nil {
		t.Fatal("clear operational history should fail")
	}
	got, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load rolled back state: %v", err)
	}
	if len(got.Tasks) != 1 || len(got.OperationLogs) != 1 || len(got.HealthSnapshots) != 1 {
		t.Fatal("app_state did not roll back")
	}
	if count := maintenanceTableCount(t, ctx, store, "platform_runtime_status"); count != 1 {
		t.Fatalf("early table delete did not roll back: %d", count)
	}
	if count := maintenanceTableCount(t, ctx, store, "metric_samples"); count != 1 {
		t.Fatalf("metric sample count = %d, want 1", count)
	}
	if value, err := store.GetSecretMaterial(ctx, "secret_restore_rollback"); err != nil || value != "rollback-session-key" {
		t.Fatal("restore session secret deletion did not roll back")
	}
}

func TestFactoryResetRollsBackEveryTableWhenStateWriteFails(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTestStore(t, ctx)
	defer func() {
		_ = store.Close()
	}()

	if err := store.ReplaceState(ctx, populatedMaintenanceState()); err != nil {
		t.Fatalf("replace state: %v", err)
	}
	if err := store.UpsertSecretMaterial(ctx, "secret_ssh", "private-key"); err != nil {
		t.Fatalf("upsert secret: %v", err)
	}
	seedMaintenanceTables(t, ctx, store)
	maintenanceExec(t, ctx, store, `CREATE TRIGGER fail_app_state_delete
		BEFORE DELETE ON app_state
		BEGIN
			SELECT RAISE(ABORT, 'forced factory reset failure');
		END`)

	fresh := domain.EmptyState()
	if err := store.FactoryReset(ctx, fresh); err == nil {
		t.Fatal("factory reset should fail")
	}
	got, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load rolled back state: %v", err)
	}
	if len(got.Projects) != 2 || len(got.Tasks) != 1 {
		t.Fatal("app_state changed after failed factory reset")
	}
	if count := maintenanceTableCount(t, ctx, store, "secret_materials"); count != 1 {
		t.Fatalf("secret deletion did not roll back: %d", count)
	}
	if count := maintenanceTableCount(t, ctx, store, "metric_samples"); count != 1 {
		t.Fatalf("normalized history deletion did not roll back: %d", count)
	}
	if count := maintenanceTableCount(t, ctx, store, "domains"); count != 1 {
		t.Fatalf("normalized business deletion did not roll back: %d", count)
	}
}

func populatedMaintenanceState() domain.State {
	state := domain.EmptyState()
	state.Servers = []domain.Object{{
		"id":               "srv_1",
		"name":             "Server",
		"publicHost":       "192.0.2.10",
		"projectIds":       []string{"prj_1"},
		"secretAssetIds":   []string{"asset_1"},
		"healthSnapshotId": "health_1",
	}}
	state.ServerRoles = []domain.Object{{"id": "role_1", "serverId": "srv_1", "role": "manager"}}
	state.SecretAssets = []domain.Object{{"id": "asset_1", "name": "SSH", "secretRef": "secret_ssh"}}
	state.Projects = []domain.Object{{"id": "prj_1", "name": "Project", "slug": "project", "serverIds": []string{"srv_1"}, "applicationIds": []string{"app_1"}}}
	state.Applications = []domain.Object{{"id": "app_1", "name": "Application", "projectId": "prj_1", "sourceConfig": domain.Object{"authSecretRef": "secret_ssh"}}}
	state.ApplicationDeploymentConfigs = []domain.Object{{
		"id":              "deploy_1",
		"applicationId":   "app_1",
		"gatewayServerId": "srv_1",
		"placement":       domain.Object{"eligibleServerIds": []string{"srv_1"}},
	}}
	state.ApplicationInstances = []domain.Object{{"id": "inst_1", "projectId": "prj_1", "applicationId": "app_1", "serverId": "srv_1", "releasePlanId": "rel_1"}}
	state.Domains = []domain.Object{{"id": "domain_1", "name": "example.com", "certificateId": "cert_1"}}
	state.Certificates = []domain.Object{{"id": "cert_1", "name": "Certificate", "crtSecretRef": "secret_crt", "keySecretRef": "secret_key"}}
	state.Routes = []domain.Object{{"id": "route_1", "projectId": "prj_1", "applicationId": "app_1", "domains": []domain.Object{{"id": "domain_1", "name": "example.com"}}}}
	state.ReleasePlans = []domain.Object{{"id": "rel_1", "projectId": "prj_1", "applicationId": "app_1", "gatewayServerId": "srv_1", "taskId": "task_1"}}
	state.RuntimeLogStreams = []domain.Object{{"id": "stream_1", "targetKind": "application", "targetId": "app_1"}}
	state.MonitoringAgents = []domain.Object{{
		"id":              "agent_1",
		"serverId":        "srv_1",
		"status":          "online",
		"lastHeartbeatAt": "2026-07-14T00:00:00Z",
		"lastSampleAt":    "2026-07-14T00:00:00Z",
		"lastError":       "old error",
	}}
	state.FrpTunnels = []domain.FrpTunnel{{ID: "tunnel_1", Name: "Tunnel", ServerID: "srv_1"}}
	state.FrpTunnelRuns = []domain.FrpTunnelRun{{ID: "run_1", TunnelID: "tunnel_1", ServerID: "srv_1", Status: domain.FrpTunnelStatusStopped, StartOperationRunID: "task_1"}}
	state.HealthSnapshots = []domain.Object{{"id": "health_1", "targetKind": "server", "targetId": "srv_1"}}
	state.MetricSamples = []domain.Object{{"id": "metric_state_1", "serverId": "srv_1"}}
	state.MetricAggregates = []domain.Object{{"id": "aggregate_state_1", "serverId": "srv_1"}}
	state.SwarmSnapshots = []domain.Object{{"id": "swarm_state_1", "serverId": "srv_1"}}
	state.Tasks = []domain.Task{{ID: "task_1", Action: "deploy_application", Status: "succeeded", TargetKind: "application", TargetID: "app_1", Payload: map[string]any{}, CreatedAt: "2026-07-14T00:00:00Z", UpdatedAt: "2026-07-14T00:00:00Z"}}
	state.ConfirmationTokens = []domain.Confirmation{{ID: "confirm_1", TaskID: "task_1", Action: "deploy_application", Status: "confirmed", Token: "token", CreatedAt: "2026-07-14T00:00:00Z", ExpiresAt: "2026-07-14T00:05:00Z"}}
	state.RuntimeLogs = []domain.RuntimeLog{{ID: "log_1", OperationRunID: "task_1", Source: "worker", Phase: "done", Level: "info", Message: "done", Timestamp: "2026-07-14T00:00:00Z"}}
	state.OperationLogs = []domain.OperationLog{{ID: "operation_log_1", OperationRunID: "task_1", Action: "deploy_application", Status: "succeeded", Timestamp: "2026-07-14T00:00:00Z"}}
	state.BuildLogs = []domain.Object{{"id": "build_1", "taskId": "task_1"}}
	state.JenkinsReleaseRuns = []domain.Object{{"id": "jenkins_1", "status": "succeeded", "taskId": "task_1"}}
	state.BackupRestoreJobs = []domain.Object{{"id": "backup_1", "status": "succeeded"}}
	return state
}

func seedMaintenanceTables(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	maintenanceExec(t, ctx, store, `INSERT INTO platform_runtime_status
		(component, status, summary, sampled_at, metadata)
		VALUES ('sqlite', 'success', 'ok', '2026-07-14T00:00:00Z', '{}')`)
	maintenanceExec(t, ctx, store, `INSERT INTO monitoring_agents
		(id, server_id, token_hash, version, protocol_version, status, capabilities, installed_at, last_heartbeat_at, last_sample_at, last_error)
		VALUES ('agent_1', 'srv_1', 'hash', '1.0.0', 1, 'online', '[]', '2026-07-14T00:00:00Z', '2026-07-14T00:00:00Z', '2026-07-14T00:00:00Z', 'old error')`)
	maintenanceExec(t, ctx, store, `INSERT INTO metric_samples
		(id, server_id, target_kind, target_id, sampled_at, "values", labels)
		VALUES ('metric_1', 'srv_1', 'server', 'srv_1', '2026-07-14T00:00:00Z', '{}', '{}')`)
	maintenanceExec(t, ctx, store, `INSERT INTO metric_aggregates
		(id, server_id, target_kind, target_id, "window", bucket_start, "values", labels, sample_count)
		VALUES ('aggregate_1', 'srv_1', 'server', 'srv_1', '1h', '2026-07-14T00:00:00Z', '{}', '{}', 1)`)
	maintenanceExec(t, ctx, store, `INSERT INTO swarm_snapshots
		(id, server_id, sampled_at, nodes, services, tasks, networks, summary, status)
		VALUES ('swarm_1', 'srv_1', '2026-07-14T00:00:00Z', '[]', '[]', '[]', '[]', 'snapshot', 'success')`)
	maintenanceExec(t, ctx, store, `INSERT INTO domains
		(id, name, data) VALUES ('domain_1', 'example.com', '{}')`)
	maintenanceExec(t, ctx, store, `INSERT INTO routes
		(id, project_id, application_id, data) VALUES ('route_1', 'prj_1', 'app_1', '{}')`)
	maintenanceExec(t, ctx, store, `INSERT INTO process_runs
		(id, kind, status, subject, decision, metrics, generated_at, report)
		VALUES ('process_1', 'task', 'passed', '{}', '{}', '{}', '2026-07-14T00:00:00Z', '{}')`)
	maintenanceExec(t, ctx, store, `INSERT INTO process_checks
		(id, process_id, code, category, phase, target, outcome, severity, blocking, summary, details, observed_at)
		VALUES ('check_1', 'process_1', 'OK', 'test', 'test', '{}', 'passed', 'info', 0, 'ok', '{}', '2026-07-14T00:00:00Z')`)
	maintenanceExec(t, ctx, store, `INSERT INTO process_resources
		(process_id, resource_kind, resource_id, resource_name, resource_scope, labels)
		VALUES ('process_1', 'server', 'srv_1', 'Server', '', '{}')`)
	maintenanceExec(t, ctx, store, `INSERT INTO resource_inventory
		(resource_kind, resource_id, resource_name, resource_scope, labels, updated_at)
		VALUES ('stale', 'stale_1', 'Stale', '', '{}', '2026-07-14T00:00:00Z')`)
	maintenanceExec(t, ctx, store, `INSERT INTO resource_links
		(source_kind, source_id, target_kind, target_id, relation, metadata, updated_at)
		VALUES ('stale', 'stale_1', 'server', 'srv_1', 'stale', '{}', '2026-07-14T00:00:00Z')`)
	maintenanceExec(t, ctx, store, `INSERT INTO frp_probe_hits
		(id, tunnel_id, run_id, mapping_name, method, path, remote_addr, received_at)
		VALUES ('hit_1', 'tunnel_1', 'run_1', 'http', 'GET', '/', '127.0.0.1', '2026-07-14T00:00:00Z')`)
	maintenanceExec(t, ctx, store, `INSERT INTO frp_probe_stats
		(tunnel_id, hit_count, updated_at) VALUES ('tunnel_1', 1, '2026-07-14T00:00:00Z')`)
}

func openMaintenanceTestStore(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "iquops.db"), "../../../../")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store
}

func maintenanceExec(t *testing.T, ctx context.Context, store *Store, statement string, args ...any) {
	t.Helper()
	if _, err := store.db.ExecContext(ctx, statement, args...); err != nil {
		t.Fatal("could not apply maintenance fixture")
	}
}

func maintenanceTableCount(t *testing.T, ctx context.Context, store *Store, table string) int {
	t.Helper()
	return maintenanceRowCount(t, ctx, store, "SELECT COUNT(*) FROM "+quoteSQLiteIdentifier(table))
}

func maintenanceRowCount(t *testing.T, ctx context.Context, store *Store, query string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		t.Fatal("maintenance row count unavailable")
	}
	return count
}

func maintenanceMutableTables(t *testing.T, ctx context.Context, store *Store) []string {
	t.Helper()
	rows, err := store.db.QueryContext(ctx, `SELECT name FROM sqlite_schema
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name NOT IN ('_migrations', 'app_state')
		ORDER BY name`)
	if err != nil {
		t.Fatalf("list mutable tables: %v", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	var result []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan mutable table: %v", err)
		}
		result = append(result, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate mutable tables: %v", err)
	}
	return result
}

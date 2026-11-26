package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// ClearOperationalHistory removes local operational history while preserving
// declarative business resources and secret material. All state and normalized
// table changes commit or roll back together.
func (s *Store) ClearOperationalHistory(ctx context.Context) error {
	return s.withImmediateMaintenanceTransaction(ctx, func(conn *sql.Conn) error {
		state, err := loadStateConn(ctx, conn)
		if err != nil {
			return err
		}
		restoreSessionSecretRefs := removableRestoreSessionSecretRefs(state)
		clearOperationalHistoryState(&state)
		if err := writeCurrentStateConn(ctx, conn, state); err != nil {
			return err
		}
		for _, secretRef := range restoreSessionSecretRefs {
			if _, err := conn.ExecContext(ctx, "DELETE FROM secret_materials WHERE secret_ref = ?", secretRef); err != nil {
				return err
			}
		}

		statements := []string{
			"DELETE FROM operation_read_versions",
			"DELETE FROM process_checks",
			"DELETE FROM process_resources",
			"DELETE FROM process_runs",
			"DELETE FROM frp_probe_hits",
			"DELETE FROM frp_probe_stats",
			"DELETE FROM metric_samples",
			"DELETE FROM metric_aggregates",
			"DELETE FROM swarm_snapshots",
			"DELETE FROM platform_runtime_status",
			"DELETE FROM resource_links",
			"DELETE FROM resource_inventory",
		}
		for _, statement := range statements {
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, `UPDATE monitoring_agents
			SET status = 'offline',
			    last_heartbeat_at = NULL,
			    last_sample_at = NULL,
			    last_error = NULL,
			    updated_at = ?`, domain.Now()); err != nil {
			return err
		}
		return rebuildRetainedResourceIndex(ctx, conn, state)
	})
}

// FactoryReset replaces app_state with a caller-provided fresh-install state
// and clears every mutable SQLite table. Schema and migration records remain.
func (s *Store) FactoryReset(ctx context.Context, freshState domain.State) error {
	normalized, err := cloneNormalizedState(freshState)
	if err != nil {
		return err
	}
	return s.withImmediateMaintenanceTransaction(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
			return err
		}
		tables, err := mutableTableNames(ctx, conn)
		if err != nil {
			return err
		}
		for _, table := range tables {
			if _, err := conn.ExecContext(ctx, "DELETE FROM "+quoteSQLiteIdentifier(table)); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, "DELETE FROM app_state"); err != nil {
			return err
		}
		return writeCurrentStateConn(ctx, conn, normalized)
	})
}

func (s *Store) DataMaintenanceCounts(ctx context.Context) (map[string]int, error) {
	tables := map[string]string{
		"secretMaterials":              "secret_materials",
		"monitoringAgentRegistrations": "monitoring_agents",
		"metricSamples":                "metric_samples",
		"metricAggregates":             "metric_aggregates",
		"swarmSnapshots":               "swarm_snapshots",
		"processReports":               "process_runs",
		"frpProbeHits":                 "frp_probe_hits",
		"platformProbeCache":           "platform_runtime_status",
	}
	result := make(map[string]int, len(tables))
	for key, table := range tables {
		var count int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteSQLiteIdentifier(table)).Scan(&count); err != nil {
			return nil, err
		}
		result[key] = count
	}
	return result, nil
}

func (s *Store) withImmediateMaintenanceTransaction(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Close()
	}()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err := checkMaintenanceGuard(ctx, conn); err != nil {
		return err
	}
	if err := fn(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func clearOperationalHistoryState(state *domain.State) {
	state.HealthSnapshots = []domain.Object{}
	state.MetricSamples = []domain.Object{}
	state.MetricAggregates = []domain.Object{}
	state.SwarmSnapshots = []domain.Object{}
	state.FrpTunnelRuns = []domain.FrpTunnelRun{}
	state.LegacyFrpTunnelSessions = nil
	state.RuntimeLogs = []domain.RuntimeLog{}
	state.BuildLogs = []domain.Object{}
	state.OperationLogs = []domain.OperationLog{}
	state.OperationRuns = []domain.OperationRun{}
	state.JenkinsReleaseRuns = []domain.Object{}
	state.ConfirmationTokens = []domain.Confirmation{}
	state.Tasks = []domain.Task{}
	state.BackupRestoreJobs = []domain.Object{}
	state.LegacyImportExportJobs = nil

	for _, agent := range state.MonitoringAgents {
		agent["status"] = "offline"
		delete(agent, "lastHeartbeatAt")
		delete(agent, "lastSampleAt")
		delete(agent, "lastError")
		delete(agent, "lastErrorSummary")
	}
	stripOperationalReferencesFromState(state)
	state.EnsureCollections()
}

func removableRestoreSessionSecretRefs(state domain.State) []string {
	businessRefs := map[string]bool{}
	collections := [][]domain.Object{
		state.Servers,
		state.ServerRoles,
		state.SecretAssets,
		state.Domains,
		state.Certificates,
		state.Resolvers,
		state.Ingresses,
		state.MonitoringAgents,
		state.Projects,
		state.Applications,
		state.ApplicationInstances,
		state.ApplicationDeploymentConfigs,
		state.Routes,
		state.ReleasePlans,
		state.RuntimeLogStreams,
	}
	for _, collection := range collections {
		for _, item := range collection {
			walkSecretReferences(item, func(secretRef string) {
				businessRefs[secretRef] = true
			})
		}
	}
	refs := map[string]bool{}
	for _, job := range state.BackupRestoreJobs {
		if domain.StringValue(job["kind"]) != "restore_upload" {
			continue
		}
		secretRef := strings.TrimSpace(domain.StringValue(job["sessionSecretRef"]))
		if secretRef != "" && !businessRefs[secretRef] {
			refs[secretRef] = true
		}
	}
	result := make([]string, 0, len(refs))
	for secretRef := range refs {
		result = append(result, secretRef)
	}
	sort.Strings(result)
	return result
}

func stripOperationalReferencesFromState(state *domain.State) {
	collections := [][]domain.Object{
		state.Servers,
		state.ServerRoles,
		state.SecretAssets,
		state.Domains,
		state.Certificates,
		state.Resolvers,
		state.Ingresses,
		state.MonitoringAgents,
		state.Projects,
		state.Applications,
		state.ApplicationInstances,
		state.ApplicationDeploymentConfigs,
		state.Routes,
		state.ReleasePlans,
		state.RuntimeLogStreams,
	}
	for _, collection := range collections {
		for _, item := range collection {
			stripOperationalReferences(item)
		}
	}
}

func stripOperationalReferences(value any) {
	switch typed := value.(type) {
	case domain.Object:
		stripOperationalReferenceMap(map[string]any(typed))
	case map[string]any:
		stripOperationalReferenceMap(typed)
	case []domain.Object:
		for _, item := range typed {
			stripOperationalReferences(item)
		}
	case []map[string]any:
		for _, item := range typed {
			stripOperationalReferences(item)
		}
	case []any:
		for _, item := range typed {
			stripOperationalReferences(item)
		}
	}
}

func stripOperationalReferenceMap(value map[string]any) {
	for key, nested := range value {
		if isOperationalHistoryReference(key) {
			delete(value, key)
			continue
		}
		stripOperationalReferences(nested)
	}
}

func isOperationalHistoryReference(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "taskid", "lasttaskid", "starttaskid", "stoptaskid",
		"confirmationtokenid", "logref", "processid", "lastprocessid",
		"healthsnapshotid", "lasthealthsnapshotid",
		"metricsampleid", "lastmetricsampleid",
		"swarmsnapshotid", "lastswarmsnapshotid",
		"frprunid", "lastfrprunid", "probehitid", "lastprobehitid":
		return true
	default:
		return false
	}
}

func cloneNormalizedState(state domain.State) (domain.State, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return domain.EmptyState(), err
	}
	return domain.NormalizeState(data)
}

func writeCurrentStateConn(ctx context.Context, conn *sql.Conn, state domain.State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO app_state (id, state, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`,
		currentStateID, string(data), domain.Now())
	return err
}

func mutableTableNames(ctx context.Context, conn *sql.Conn) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT name
		FROM sqlite_schema
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name NOT IN ('_migrations', 'app_state')
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

type retainedResource struct {
	Kind  string
	ID    string
	Name  string
	Scope string
}

type retainedResourceLink struct {
	SourceKind string
	SourceID   string
	TargetKind string
	TargetID   string
	Relation   string
}

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type operationalHistoryRestore struct {
	CheckReports []domain.CheckReport
	FrpProbeHits []domain.FrpProbeHit
}

func (s *Store) ListCheckReports(ctx context.Context) ([]domain.CheckReport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, report
		FROM process_runs
		ORDER BY generated_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	result := []domain.CheckReport{}
	for rows.Next() {
		var id string
		var payload string
		if err := rows.Scan(&id, &payload); err != nil {
			return nil, err
		}
		var report domain.CheckReport
		if err := json.Unmarshal([]byte(payload), &report); err != nil {
			return nil, fmt.Errorf("PROCESS_REPORT_DECODE_FAILED:%s: %w", id, err)
		}
		result = append(result, report)
	}
	return result, rows.Err()
}

func (s *Store) ListFrpProbeHitsForTunnels(ctx context.Context, tunnelIDs []string) ([]domain.FrpProbeHit, error) {
	tunnelIDs = uniqueSortedNonEmpty(tunnelIDs)
	if len(tunnelIDs) == 0 {
		return []domain.FrpProbeHit{}, nil
	}
	placeholders := make([]string, len(tunnelIDs))
	args := make([]any, len(tunnelIDs))
	for index, tunnelID := range tunnelIDs {
		placeholders[index] = "?"
		args[index] = tunnelID
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, tunnel_id, run_id, mapping_name, method, host, path, raw_query, remote_addr, user_agent, received_at
		FROM frp_probe_hits
		WHERE tunnel_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY tunnel_id, received_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	result := []domain.FrpProbeHit{}
	for rows.Next() {
		var hit domain.FrpProbeHit
		if err := rows.Scan(
			&hit.ID,
			&hit.TunnelID,
			&hit.RunID,
			&hit.MappingName,
			&hit.Method,
			&hit.Host,
			&hit.Path,
			&hit.RawQuery,
			&hit.RemoteAddr,
			&hit.UserAgent,
			&hit.ReceivedAt,
		); err != nil {
			return nil, err
		}
		hit.NormalizeLog()
		result = append(result, hit)
	}
	return result, rows.Err()
}

func restoreOperationalHistoryConn(ctx context.Context, conn *sql.Conn, history operationalHistoryRestore) error {
	for _, report := range history.CheckReports {
		if err := upsertTerminalCheckReportConn(ctx, conn, report); err != nil {
			return err
		}
	}
	affectedTunnels := map[string]bool{}
	for _, hit := range history.FrpProbeHits {
		if err := upsertFrpProbeHitConn(ctx, conn, hit, affectedTunnels); err != nil {
			return err
		}
	}
	return rebuildAffectedFrpProbeStatsConn(ctx, conn, affectedTunnels)
}

func upsertTerminalCheckReportConn(ctx context.Context, conn *sql.Conn, report domain.CheckReport) error {
	if strings.TrimSpace(report.ID) == "" || strings.TrimSpace(report.Kind) == "" || strings.TrimSpace(report.GeneratedAt) == "" {
		return errors.New("PROCESS_REPORT_INVALID")
	}
	if !isTerminalCheckStatus(report.Status) {
		return fmt.Errorf("PROCESS_REPORT_NOT_TERMINAL:%s", report.ID)
	}
	subject, err := json.Marshal(report.Subject)
	if err != nil {
		return err
	}
	decision, err := json.Marshal(report.Decision)
	if err != nil {
		return err
	}
	metrics, err := json.Marshal(report.Metrics)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO process_runs
		(id, kind, status, subject, decision, metrics, started_at, completed_at, generated_at, report)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  kind = excluded.kind,
		  status = excluded.status,
		  subject = excluded.subject,
		  decision = excluded.decision,
		  metrics = excluded.metrics,
		  started_at = excluded.started_at,
		  completed_at = excluded.completed_at,
		  generated_at = excluded.generated_at,
		  report = excluded.report`,
		report.ID,
		report.Kind,
		report.Status,
		string(subject),
		string(decision),
		string(metrics),
		nullString(report.StartedAt),
		nullString(report.CompletedAt),
		report.GeneratedAt,
		string(payload),
	); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "DELETE FROM process_checks WHERE process_id = ?", report.ID); err != nil {
		return err
	}
	for _, check := range report.Checks {
		if strings.TrimSpace(check.ID) == "" {
			return fmt.Errorf("PROCESS_CHECK_INVALID:%s", report.ID)
		}
		target, err := json.Marshal(check.Target)
		if err != nil {
			return err
		}
		details, err := json.Marshal(check.Details)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO process_checks
			(id, process_id, code, category, phase, target, outcome, severity, blocking, summary, details, observed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			  process_id = excluded.process_id,
			  code = excluded.code,
			  category = excluded.category,
			  phase = excluded.phase,
			  target = excluded.target,
			  outcome = excluded.outcome,
			  severity = excluded.severity,
			  blocking = excluded.blocking,
			  summary = excluded.summary,
			  details = excluded.details,
			  observed_at = excluded.observed_at`,
			check.ID,
			report.ID,
			check.Code,
			check.Category,
			check.Phase,
			string(target),
			check.Outcome,
			check.Severity,
			boolInt(check.Blocking),
			check.Summary,
			string(details),
			check.ObservedAt,
		); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, "DELETE FROM process_resources WHERE process_id = ?", report.ID); err != nil {
		return err
	}
	for _, ref := range report.Resources {
		ref = domain.NormalizeResourceRef(ref)
		if ref.Kind == "" || ref.ID == "" {
			continue
		}
		labels, err := json.Marshal(ref.Labels)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO process_resources
			(process_id, resource_kind, resource_id, resource_name, resource_scope, labels)
			VALUES (?, ?, ?, ?, ?, ?)`, report.ID, ref.Kind, ref.ID, ref.Name, ref.Scope, string(labels)); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO resource_inventory
			(resource_kind, resource_id, resource_name, resource_scope, labels, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(resource_kind, resource_id) DO UPDATE SET
			  resource_name = excluded.resource_name,
			  resource_scope = excluded.resource_scope,
			  labels = excluded.labels,
			  updated_at = excluded.updated_at`, ref.Kind, ref.ID, ref.Name, ref.Scope, string(labels), domain.Now()); err != nil {
			return err
		}
	}
	return nil
}

func upsertFrpProbeHitConn(ctx context.Context, conn *sql.Conn, hit domain.FrpProbeHit, affectedTunnels map[string]bool) error {
	if hit.ID == "" || hit.TunnelID == "" || hit.RunID == "" || hit.MappingName == "" || hit.Method == "" || hit.Path == "" || hit.RemoteAddr == "" || hit.ReceivedAt == "" {
		return errors.New("FRP_PROBE_HIT_INVALID")
	}
	hit.NormalizeLog()
	var existingTunnelID string
	err := conn.QueryRowContext(ctx, "SELECT tunnel_id FROM frp_probe_hits WHERE id = ?", hit.ID).Scan(&existingTunnelID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if existingTunnelID != "" {
		affectedTunnels[existingTunnelID] = true
	}
	affectedTunnels[hit.TunnelID] = true
	_, err = conn.ExecContext(ctx, `INSERT INTO frp_probe_hits
		(id, tunnel_id, run_id, mapping_name, method, host, path, raw_query, remote_addr, user_agent, received_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  tunnel_id = excluded.tunnel_id,
		  run_id = excluded.run_id,
		  mapping_name = excluded.mapping_name,
		  method = excluded.method,
		  host = excluded.host,
		  path = excluded.path,
		  raw_query = excluded.raw_query,
		  remote_addr = excluded.remote_addr,
		  user_agent = excluded.user_agent,
		  received_at = excluded.received_at`,
		hit.ID,
		hit.TunnelID,
		hit.RunID,
		hit.MappingName,
		hit.Method,
		hit.Host,
		hit.Path,
		hit.RawQuery,
		hit.RemoteAddr,
		hit.UserAgent,
		hit.ReceivedAt,
	)
	return err
}

func rebuildAffectedFrpProbeStatsConn(ctx context.Context, conn *sql.Conn, affectedTunnels map[string]bool) error {
	tunnelIDs := make([]string, 0, len(affectedTunnels))
	for tunnelID := range affectedTunnels {
		if tunnelID = strings.TrimSpace(tunnelID); tunnelID != "" {
			tunnelIDs = append(tunnelIDs, tunnelID)
		}
	}
	sort.Strings(tunnelIDs)
	for _, tunnelID := range tunnelIDs {
		var count int
		var lastHitAt sql.NullString
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*), MAX(received_at)
			FROM frp_probe_hits WHERE tunnel_id = ?`, tunnelID).Scan(&count, &lastHitAt); err != nil {
			return err
		}
		var lastHit any
		if lastHitAt.Valid {
			lastHit = lastHitAt.String
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO frp_probe_stats
			(tunnel_id, hit_count, last_hit_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(tunnel_id) DO UPDATE SET
			  hit_count = excluded.hit_count,
			  last_hit_at = excluded.last_hit_at,
			  updated_at = excluded.updated_at`, tunnelID, count, lastHit, domain.Now()); err != nil {
			return err
		}
	}
	return nil
}

func isTerminalCheckStatus(status string) bool {
	switch status {
	case domain.CheckStatusPassed,
		domain.CheckStatusWarning,
		domain.CheckStatusBlocked,
		domain.CheckStatusFailed,
		domain.CheckStatusCancelled:
		return true
	default:
		return false
	}
}

func uniqueSortedNonEmpty(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

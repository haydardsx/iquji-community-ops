package sqlite

import (
	"context"
	"encoding/json"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (s *Store) CreateProcessRun(ctx context.Context, report domain.CheckReport) error {
	return s.upsertProcessRun(ctx, report)
}

func (s *Store) CompleteProcessRun(ctx context.Context, report domain.CheckReport) error {
	return s.upsertProcessRun(ctx, report)
}

func (s *Store) AppendCheckItem(ctx context.Context, processID string, check domain.CheckItem) error {
	target, err := json.Marshal(check.Target)
	if err != nil {
		return err
	}
	details, err := json.Marshal(check.Details)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO process_checks
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
		check.ID, processID, check.Code, check.Category, check.Phase, string(target), check.Outcome, check.Severity, boolInt(check.Blocking), check.Summary, string(details), check.ObservedAt)
	return err
}

func (s *Store) UpsertResourceRef(ctx context.Context, ref domain.ResourceRef) error {
	ref = domain.NormalizeResourceRef(ref)
	if ref.Kind == "" || ref.ID == "" {
		return nil
	}
	labels, err := json.Marshal(ref.Labels)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO resource_inventory
		(resource_kind, resource_id, resource_name, resource_scope, labels, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(resource_kind, resource_id) DO UPDATE SET
		  resource_name = excluded.resource_name,
		  resource_scope = excluded.resource_scope,
		  labels = excluded.labels,
		  updated_at = excluded.updated_at`,
		ref.Kind, ref.ID, ref.Name, ref.Scope, string(labels), domain.Now())
	return err
}

func (s *Store) UpsertResourceLink(ctx context.Context, source domain.ResourceRef, target domain.ResourceRef, relation string, metadata domain.Object) error {
	source = domain.NormalizeResourceRef(source)
	target = domain.NormalizeResourceRef(target)
	if source.Kind == "" || source.ID == "" || target.Kind == "" || target.ID == "" || relation == "" {
		return nil
	}
	if err := s.UpsertResourceRef(ctx, source); err != nil {
		return err
	}
	if err := s.UpsertResourceRef(ctx, target); err != nil {
		return err
	}
	if metadata == nil {
		metadata = domain.Object{}
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO resource_links
		(source_kind, source_id, target_kind, target_id, relation, metadata, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_kind, source_id, target_kind, target_id, relation) DO UPDATE SET
		  metadata = excluded.metadata,
		  updated_at = excluded.updated_at`,
		source.Kind, source.ID, target.Kind, target.ID, relation, string(payload), domain.Now())
	return err
}

func (s *Store) ListResourceGraph(ctx context.Context, ref domain.ResourceRef) ([]domain.ResourceLink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		  source_kind, source_id, source.resource_name, source.resource_scope, source.labels,
		  target_kind, target_id, target.resource_name, target.resource_scope, target.labels,
		  relation, metadata
		FROM resource_links
		LEFT JOIN resource_inventory AS source
		  ON source.resource_kind = source_kind AND source.resource_id = source_id
		LEFT JOIN resource_inventory AS target
		  ON target.resource_kind = target_kind AND target.resource_id = target_id
		WHERE (source_kind = ? AND source_id = ?) OR (target_kind = ? AND target_id = ?)
		ORDER BY relation, target_kind, target_id`, ref.Kind, ref.ID, ref.Kind, ref.ID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var links []domain.ResourceLink
	for rows.Next() {
		var source domain.ResourceRef
		var target domain.ResourceRef
		var sourceLabels, targetLabels, metadata string
		var link domain.ResourceLink
		if err := rows.Scan(
			&source.Kind, &source.ID, &source.Name, &source.Scope, &sourceLabels,
			&target.Kind, &target.ID, &target.Name, &target.Scope, &targetLabels,
			&link.Relation, &metadata,
		); err != nil {
			return nil, err
		}
		source.Labels = decodeLabels(sourceLabels)
		target.Labels = decodeLabels(targetLabels)
		link.Source = source
		link.Target = target
		link.Metadata = domain.Object{}
		_ = json.Unmarshal([]byte(metadata), &link.Metadata)
		links = append(links, link)
	}
	return links, rows.Err()
}

func (s *Store) upsertProcessRun(ctx context.Context, report domain.CheckReport) error {
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
	if _, err := s.db.ExecContext(ctx, `INSERT INTO process_runs
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
		report.ID, report.Kind, report.Status, string(subject), string(decision), string(metrics), nullString(report.StartedAt), nullString(report.CompletedAt), report.GeneratedAt, string(payload)); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM process_checks WHERE process_id = ?", report.ID); err != nil {
		return err
	}
	for _, check := range report.Checks {
		if err := s.AppendCheckItem(ctx, report.ID, check); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM process_resources WHERE process_id = ?", report.ID); err != nil {
		return err
	}
	for _, ref := range report.Resources {
		if err := s.upsertProcessResource(ctx, report.ID, ref); err != nil {
			return err
		}
		if err := s.UpsertResourceRef(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) upsertProcessResource(ctx context.Context, processID string, ref domain.ResourceRef) error {
	ref = domain.NormalizeResourceRef(ref)
	if ref.Kind == "" || ref.ID == "" {
		return nil
	}
	labels, err := json.Marshal(ref.Labels)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO process_resources
		(process_id, resource_kind, resource_id, resource_name, resource_scope, labels)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(process_id, resource_kind, resource_id) DO UPDATE SET
		  resource_name = excluded.resource_name,
		  resource_scope = excluded.resource_scope,
		  labels = excluded.labels`,
		processID, ref.Kind, ref.ID, ref.Name, ref.Scope, string(labels))
	return err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func decodeLabels(value string) map[string]string {
	labels := map[string]string{}
	if value == "" {
		return labels
	}
	_ = json.Unmarshal([]byte(value), &labels)
	return labels
}

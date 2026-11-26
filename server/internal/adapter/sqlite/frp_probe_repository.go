package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const DefaultFrpProbeMaxHits = 200

type FrpProbeHitPage struct {
	Items    []domain.FrpProbeHit `json:"items"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"pageSize"`
	Total    int                  `json:"total"`
	HitCount int                  `json:"hitCount"`
	MaxHits  int                  `json:"maxHits"`
}

func (s *Store) RecordFrpProbeHit(ctx context.Context, hit domain.FrpProbeHit, maxHits int) (int, error) {
	if hit.ID == "" || hit.TunnelID == "" || hit.RunID == "" || hit.MappingName == "" || hit.Method == "" || hit.Path == "" || hit.RemoteAddr == "" || hit.ReceivedAt == "" {
		return 0, errors.New("FRP_PROBE_HIT_INVALID")
	}
	hit.NormalizeLog()
	if maxHits <= 0 {
		maxHits = DefaultFrpProbeMaxHits
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.ExecContext(ctx, `INSERT INTO frp_probe_hits
		(id, tunnel_id, run_id, mapping_name, method, host, path, raw_query, remote_addr, user_agent, received_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hit.ID, hit.TunnelID, hit.RunID, hit.MappingName, hit.Method, hit.Host, hit.Path, hit.RawQuery, hit.RemoteAddr, hit.UserAgent, hit.ReceivedAt); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO frp_probe_stats
		(tunnel_id, hit_count, last_hit_at, updated_at)
		VALUES (?, 1, ?, ?)
		ON CONFLICT(tunnel_id) DO UPDATE SET
		  hit_count = frp_probe_stats.hit_count + 1,
		  last_hit_at = excluded.last_hit_at,
		  updated_at = excluded.updated_at`, hit.TunnelID, hit.ReceivedAt, hit.ReceivedAt); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM frp_probe_hits
		WHERE tunnel_id = ? AND id NOT IN (
		  SELECT id FROM frp_probe_hits WHERE tunnel_id = ?
		  ORDER BY received_at DESC, id DESC LIMIT ?
		)`, hit.TunnelID, hit.TunnelID, maxHits); err != nil {
		return 0, err
	}
	var hitCount int
	if err := tx.QueryRowContext(ctx, "SELECT hit_count FROM frp_probe_stats WHERE tunnel_id = ?", hit.TunnelID).Scan(&hitCount); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return hitCount, nil
}

func (s *Store) ListFrpProbeHits(ctx context.Context, tunnelID string, page int, pageSize int) (FrpProbeHitPage, error) {
	if tunnelID == "" {
		return FrpProbeHitPage{}, errors.New("FRP_TUNNEL_ID_REQUIRED")
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 100
	}
	if pageSize > 100 {
		pageSize = 100
	}
	result := FrpProbeHitPage{Items: []domain.FrpProbeHit{}, Page: page, PageSize: pageSize, MaxHits: DefaultFrpProbeMaxHits}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM frp_probe_hits WHERE tunnel_id = ?", tunnelID).Scan(&result.Total); err != nil {
		return FrpProbeHitPage{}, err
	}
	err := s.db.QueryRowContext(ctx, "SELECT hit_count FROM frp_probe_stats WHERE tunnel_id = ?", tunnelID).Scan(&result.HitCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return FrpProbeHitPage{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, tunnel_id, run_id, mapping_name, method, host, path, raw_query, remote_addr, user_agent, received_at
		FROM frp_probe_hits WHERE tunnel_id = ?
		ORDER BY received_at DESC, id DESC LIMIT ? OFFSET ?`, tunnelID, pageSize, (page-1)*pageSize)
	if err != nil {
		return FrpProbeHitPage{}, err
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var hit domain.FrpProbeHit
		if err := rows.Scan(&hit.ID, &hit.TunnelID, &hit.RunID, &hit.MappingName, &hit.Method, &hit.Host, &hit.Path, &hit.RawQuery, &hit.RemoteAddr, &hit.UserAgent, &hit.ReceivedAt); err != nil {
			return FrpProbeHitPage{}, err
		}
		hit.NormalizeLog()
		result.Items = append(result.Items, hit)
	}
	return result, rows.Err()
}

func (s *Store) ClearFrpProbeHits(ctx context.Context, tunnelID string) error {
	if tunnelID == "" {
		return errors.New("FRP_TUNNEL_ID_REQUIRED")
	}
	now := domain.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.ExecContext(ctx, "DELETE FROM frp_probe_hits WHERE tunnel_id = ?", tunnelID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO frp_probe_stats
		(tunnel_id, hit_count, cleared_at, updated_at)
		VALUES (?, 0, ?, ?)
		ON CONFLICT(tunnel_id) DO UPDATE SET
		  hit_count = 0,
		  last_hit_at = NULL,
		  cleared_at = excluded.cleared_at,
		  updated_at = excluded.updated_at`, tunnelID, now, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteFrpProbeData(ctx context.Context, tunnelID string) error {
	if tunnelID == "" {
		return errors.New("FRP_TUNNEL_ID_REQUIRED")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.ExecContext(ctx, "DELETE FROM frp_probe_hits WHERE tunnel_id = ?", tunnelID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM frp_probe_stats WHERE tunnel_id = ?", tunnelID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FrpProbeHitCount(ctx context.Context, tunnelID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT hit_count FROM frp_probe_stats WHERE tunnel_id = ?", tunnelID).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return count, err
}

func (s *Store) ListFrpProbeHitCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT tunnel_id, hit_count FROM frp_probe_stats")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	result := map[string]int{}
	for rows.Next() {
		var tunnelID string
		var count int
		if err := rows.Scan(&tunnelID, &count); err != nil {
			return nil, err
		}
		result[tunnelID] = count
	}
	return result, rows.Err()
}

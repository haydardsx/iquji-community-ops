package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// OperationReadVersions 是当前控制面已成功展示的终态版本，不随业务备份导出。
func (s *Store) OperationReadVersions(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT operation_run_id, status, updated_at FROM operation_read_versions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := map[string]string{}
	for rows.Next() {
		var id, status, updatedAt string
		if err := rows.Scan(&id, &status, &updatedAt); err != nil {
			return nil, err
		}
		versions[id] = status + ":" + updatedAt
	}
	return versions, rows.Err()
}

// MarkOperationRead 在同一 SQLite 事务中校验终态版本，避免旧日志误清除新结果。
func (s *Store) MarkOperationRead(ctx context.Context, id, status, updatedAt string) error {
	return s.withImmediateMaintenanceTransaction(ctx, func(conn *sql.Conn) error {
		state, err := loadStateConn(ctx, conn)
		if err != nil {
			return err
		}
		for _, run := range state.OperationRuns {
			if run.ID != id {
				continue
			}
			if !domain.IsOperationRunStatusTerminal(run.Status) || run.Status != status || run.UpdatedAt != updatedAt {
				return errors.New("OPERATION_READ_VERSION_CHANGED")
			}
			_, err := conn.ExecContext(ctx, `INSERT INTO operation_read_versions (operation_run_id, status, updated_at)
				VALUES (?, ?, ?) ON CONFLICT(operation_run_id) DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at`, id, status, updatedAt)
			return err
		}
		return errors.New("OPERATION_RUN_NOT_FOUND")
	})
}

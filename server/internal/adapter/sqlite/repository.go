package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const currentStateID = "current"

//go:embed migrations/*.sql
var migrationFS embed.FS

type Store struct {
	db     *sql.DB
	dbPath string
	root   string
}

type PlatformStatus struct {
	Component string         `json:"component"`
	Status    string         `json:"status"`
	Summary   string         `json:"summary"`
	CheckedAt string         `json:"checkedAt"`
	Metadata  map[string]any `json:"metadata"`
}

func Open(ctx context.Context, databasePath string, repositoryRoot string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, dbPath: databasePath, root: repositoryRoot}
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) initialize(ctx context.Context) error {
	// API 与 Worker 首次并发启动时也应等待 SQLite 锁，而不是把现有库当作初始化失败。
	if _, err := s.db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return err
	}
	if err := s.runMigrations(ctx); err != nil {
		return err
	}
	return s.bootstrap(ctx)
}

func (s *Store) runMigrations(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS _migrations (id TEXT PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		return err
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		var existing string
		err := tx.QueryRowContext(ctx, "SELECT id FROM _migrations WHERE id = ? LIMIT 1", name).Scan(&existing)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO _migrations (id, applied_at) VALUES (?, ?)", name, domain.Now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) bootstrap(ctx context.Context) error {
	return s.withImmediateMaintenanceTransaction(ctx, func(conn *sql.Conn) error {
		current, err := loadStateConn(ctx, conn)
		if err != nil {
			return err
		}
		for ref, value := range migrateLegacyCertificateMaterials(&current) {
			if _, err := conn.ExecContext(ctx, `INSERT INTO secret_materials(secret_ref,value,created_at,updated_at) VALUES(?,?,?,?) ON CONFLICT(secret_ref) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, ref, value, domain.Now(), domain.Now()); err != nil {
				return err
			}
		}
		domain.MigrateApplicationSources(&current)
		domain.MigrateApplicationDeploymentLocations(&current)
		if err := migrateReferencedApplicationCredentials(ctx, conn, &current); err != nil {
			return err
		}
		current.StripUserReferences()
		return writeCurrentStateConn(ctx, conn, current)
	})
}

func migrateLegacyCertificateMaterials(state *domain.State) map[string]string {
	materials := map[string]string{}
	for index := range state.Certificates {
		certificate := state.Certificates[index]
		certificateID := domain.StringValue(certificate["id"])
		if certificateID == "" {
			certificateID = domain.NewID("cert")
			certificate["id"] = certificateID
		}
		if certificatePEM := domain.StringValue(certificate["certificatePem"]); certificatePEM != "" {
			secretRef := domain.StringValue(certificate["crtSecretRef"])
			if secretRef == "" {
				secretRef = "secret_certificate_" + certificateID + "_crt"
			}
			certificate["crtSecretRef"] = secretRef
			materials[secretRef] = certificatePEM
		}
		if privateKeyPEM := domain.StringValue(certificate["privateKeyPem"]); privateKeyPEM != "" {
			secretRef := domain.StringValue(certificate["keySecretRef"])
			if secretRef == "" {
				secretRef = "secret_certificate_" + certificateID + "_key"
			}
			certificate["keySecretRef"] = secretRef
			materials[secretRef] = privateKeyPEM
		}
		delete(certificate, "certificatePem")
		delete(certificate, "privateKeyPem")
	}
	return materials
}

func (s *Store) LoadState(ctx context.Context) (domain.State, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT state FROM app_state WHERE id = ?", currentStateID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.EmptyState(), nil
	}
	if err != nil {
		return domain.EmptyState(), err
	}
	return domain.NormalizeState([]byte(raw))
}

func (s *Store) ReplaceState(ctx context.Context, state domain.State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO app_state (id, state, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`, currentStateID, string(data), domain.Now())
	return err
}

func (s *Store) MutateState(ctx context.Context, fn func(*domain.State) error) error {
	return s.MutateStateWithSecretChanges(ctx, nil, nil, fn)
}

func (s *Store) MutateStateWithSecretMaterials(ctx context.Context, materials map[string]string, fn func(*domain.State) error) error {
	return s.MutateStateWithSecretChanges(ctx, materials, nil, fn)
}

func (s *Store) MutateStateWithSecretChanges(ctx context.Context, materials map[string]string, deleteRefs []string, fn func(*domain.State) error) error {
	return s.mutateStateWithSecretChanges(ctx, materials, deleteRefs, false, nil, fn)
}

// MutateStateWithSecretChangesAndRebuildNormalizedHistory applies state and
// secret changes and then rebuilds normalized monitoring history from the
// mutation result. The state row, secret materials, and normalized tables share
// one BEGIN IMMEDIATE transaction.
func (s *Store) MutateStateWithSecretChangesAndRebuildNormalizedHistory(ctx context.Context, materials map[string]string, deleteRefs []string, fn func(*domain.State) error) error {
	return s.mutateStateWithSecretChanges(ctx, materials, deleteRefs, true, nil, fn)
}

// MutateStateWithSecretChangesAndRestoreOperationalHistory applies a restore
// mutation and upserts the supplied terminal process reports and FRP probe hits
// without deleting unrelated history.
func (s *Store) MutateStateWithSecretChangesAndRestoreOperationalHistory(ctx context.Context, materials map[string]string, deleteRefs []string, processReports []domain.CheckReport, frpProbeHits []domain.FrpProbeHit, fn func(*domain.State) error) error {
	history := &operationalHistoryRestore{
		CheckReports: append([]domain.CheckReport(nil), processReports...),
		FrpProbeHits: append([]domain.FrpProbeHit(nil), frpProbeHits...),
	}
	return s.mutateStateWithSecretChanges(ctx, materials, deleteRefs, true, history, fn)
}

func (s *Store) mutateStateWithSecretChanges(ctx context.Context, materials map[string]string, deleteRefs []string, rebuildNormalizedHistory bool, operationalHistory *operationalHistoryRestore, fn func(*domain.State) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Close()
	}()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	}()
	state, err := loadStateConn(ctx, conn)
	if err != nil {
		return err
	}
	if err = checkTaskClaim(ctx, state); err != nil {
		return err
	}
	if err = fn(&state); err != nil {
		return err
	}
	secretRefs := make([]string, 0, len(materials))
	for secretRef := range materials {
		secretRefs = append(secretRefs, secretRef)
	}
	sort.Strings(secretRefs)
	for _, secretRef := range secretRefs {
		value := materials[secretRef]
		if secretRef == "" || value == "" {
			return errors.New("SECRET_MATERIAL_REQUIRED")
		}
		now := domain.Now()
		if _, err = conn.ExecContext(ctx, `INSERT INTO secret_materials (secret_ref, value, created_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(secret_ref) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			secretRef, value, now, now); err != nil {
			return err
		}
	}
	deleteRefs = append([]string{}, deleteRefs...)
	sort.Strings(deleteRefs)
	for _, secretRef := range deleteRefs {
		if strings.TrimSpace(secretRef) == "" {
			continue
		}
		if _, err = conn.ExecContext(ctx, "DELETE FROM secret_materials WHERE secret_ref = ?", secretRef); err != nil {
			return err
		}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO app_state (id, state, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`, currentStateID, string(data), domain.Now()); err != nil {
		return err
	}
	if rebuildNormalizedHistory {
		if err = rebuildNormalizedHistoryConn(ctx, conn, state); err != nil {
			return err
		}
	}
	if operationalHistory != nil {
		if err = restoreOperationalHistoryConn(ctx, conn, *operationalHistory); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

func (s *Store) MutateStateAndDeleteFrpTunnelData(ctx context.Context, tunnelID string, fn func(*domain.State) error) error {
	if strings.TrimSpace(tunnelID) == "" {
		return errors.New("FRP_TUNNEL_ID_REQUIRED")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Close()
	}()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	}()
	state, err := loadStateConn(ctx, conn)
	if err != nil {
		return err
	}
	if err = fn(&state); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO app_state (id, state, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`, currentStateID, string(data), domain.Now()); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "DELETE FROM frp_probe_hits WHERE tunnel_id = ?", tunnelID); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "DELETE FROM frp_probe_stats WHERE tunnel_id = ?", tunnelID); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

func loadStateConn(ctx context.Context, conn *sql.Conn) (domain.State, error) {
	var raw string
	err := conn.QueryRowContext(ctx, "SELECT state FROM app_state WHERE id = ?", currentStateID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.EmptyState(), nil
	}
	if err != nil {
		return domain.EmptyState(), err
	}
	return domain.NormalizeState([]byte(raw))
}

func (s *Store) DatabasePath() string {
	return s.dbPath
}

func (s *Store) UpsertPlatformStatus(ctx context.Context, status PlatformStatus) error {
	metadata, err := json.Marshal(status.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO platform_runtime_status
		(component, status, summary, sampled_at, metadata)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(component) DO UPDATE SET
		  status = excluded.status,
		  summary = excluded.summary,
		  sampled_at = excluded.sampled_at,
		  metadata = excluded.metadata`,
		status.Component, status.Status, status.Summary, status.CheckedAt, string(metadata))
	return err
}

func (s *Store) ListPlatformStatuses(ctx context.Context) (map[string]PlatformStatus, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT component, status, summary, sampled_at, metadata FROM platform_runtime_status")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	result := map[string]PlatformStatus{}
	for rows.Next() {
		var item PlatformStatus
		var metadata string
		if err := rows.Scan(&item.Component, &item.Status, &item.Summary, &item.CheckedAt, &metadata); err != nil {
			return nil, err
		}
		item.Metadata = map[string]any{}
		_ = json.Unmarshal([]byte(metadata), &item.Metadata)
		result[item.Component] = item
	}
	return result, rows.Err()
}

func (s *Store) UpsertSecretMaterial(ctx context.Context, secretRef string, value string) error {
	if secretRef == "" || value == "" {
		return errors.New("SECRET_MATERIAL_REQUIRED")
	}
	now := domain.Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO secret_materials (secret_ref, value, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(secret_ref) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		secretRef, value, now, now)
	return err
}

func (s *Store) GetSecretMaterial(ctx context.Context, secretRef string) (string, error) {
	if secretRef == "" {
		return "", errors.New("SECRET_REF_REQUIRED")
	}
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM secret_materials WHERE secret_ref = ? LIMIT 1", secretRef).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("SECRET_MATERIAL_NOT_FOUND")
	}
	return value, err
}

func (s *Store) HasSecretMaterial(ctx context.Context, secretRef string) (bool, error) {
	if secretRef == "" {
		return false, nil
	}
	var exists int
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM secret_materials WHERE secret_ref = ?)", secretRef).Scan(&exists)
	return exists == 1, err
}

func (s *Store) DeleteSecretMaterial(ctx context.Context, secretRef string) error {
	if secretRef == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM secret_materials WHERE secret_ref = ?", secretRef)
	return err
}

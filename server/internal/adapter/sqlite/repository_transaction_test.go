package sqlite

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"path/filepath"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestMutateStateWithSecretChangesRollsBackEveryErrorPath(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "iquops.db"), "../../../../")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	if err := store.MutateStateWithSecretChanges(ctx, map[string]string{"": "invalid"}, nil, func(state *domain.State) error {
		state.Servers = append(state.Servers, domain.Object{"id": "must_rollback"})
		return nil
	}); err == nil {
		t.Fatalf("invalid secret reference should fail")
	}
	if err := store.MutateStateWithSecretChanges(ctx, nil, nil, func(state *domain.State) error {
		state.Servers = append(state.Servers, domain.Object{"id": "also_rollback"})
		return errors.New("CALLBACK_FAILED")
	}); err == nil {
		t.Fatalf("callback error should fail")
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = append(state.Servers, domain.Object{"id": "persisted"})
		return nil
	}); err != nil {
		t.Fatalf("subsequent mutation was blocked by a leaked transaction: %v", err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(state.Servers) != 1 || domain.StringValue(state.Servers[0]["id"]) != "persisted" {
		t.Fatal("failed transactions left unexpected server records")
	}
}

func TestBootstrapMigratesLegacyCertificateMaterialsAtomically(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "iquops.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open seed database: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE app_state (
		id TEXT PRIMARY KEY,
		state TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create app_state: %v", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成迁移测试私钥：%v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("编码迁移测试私钥：%v", err)
	}
	privateKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	seed, err := json.Marshal(domain.Object{"certificates": []domain.Object{{
		"id": "cert_legacy", "name": "Legacy",
		"certificatePem": "-----BEGIN CERTIFICATE-----legacy", "privateKeyPem": privateKeyPEM,
	}}})
	if err != nil {
		t.Fatalf("编码迁移测试状态：%v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO app_state (id, state, updated_at) VALUES (?, ?, ?)`, currentStateID, string(seed), domain.Now()); err != nil {
		t.Fatalf("seed app_state: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	store, err := Open(ctx, databasePath, "../../../../")
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer func() { _ = store.Close() }()
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load migrated state: %v", err)
	}
	if len(state.Certificates) != 1 {
		t.Fatalf("certificate count = %d", len(state.Certificates))
	}
	certificate := state.Certificates[0]
	if certificate["certificatePem"] != nil || certificate["privateKeyPem"] != nil {
		t.Fatal("legacy PEM remained in app_state")
	}
	crtRef := domain.StringValue(certificate["crtSecretRef"])
	keyRef := domain.StringValue(certificate["keySecretRef"])
	if crtRef == "" || keyRef == "" {
		t.Fatal("secret refs were not created")
	}
	if value, err := store.GetSecretMaterial(ctx, crtRef); err != nil || value != "-----BEGIN CERTIFICATE-----legacy" {
		t.Fatal("certificate material mismatch")
	}
	if value, err := store.GetSecretMaterial(ctx, keyRef); err != nil || value != privateKeyPEM {
		t.Fatalf("private key material mismatch: err=%v", err)
	}
}

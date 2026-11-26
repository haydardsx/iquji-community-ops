package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func probeHit(tunnelID string, runID string, index int) domain.FrpProbeHit {
	receivedAt := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC).
		Add(time.Duration(index) * time.Second).
		Format(time.RFC3339Nano)
	return domain.FrpProbeHit{
		ID: fmt.Sprintf("probehit_%s_%03d", tunnelID, index), TunnelID: tunnelID, RunID: runID,
		MappingName: "probe", Method: "GET", Host: "probe.example.com", Path: fmt.Sprintf("/hit-%03d", index),
		RemoteAddr: "127.0.0.1:12345", UserAgent: "repository-test", ReceivedAt: receivedAt,
	}
}

func TestFrpProbeRepositoryRetainsAndClearsEachTunnelIndependently(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "iquops.db")
	store, err := Open(ctx, databasePath, "../../../../")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	for index := 0; index < 205; index++ {
		if _, err := store.RecordFrpProbeHit(ctx, probeHit("tun_1", "run_1", index), DefaultFrpProbeMaxHits); err != nil {
			t.Fatalf("record first tunnel hit %d: %v", index, err)
		}
	}
	for index := 0; index < 3; index++ {
		if _, err := store.RecordFrpProbeHit(ctx, probeHit("tun_2", "run_2", index), DefaultFrpProbeMaxHits); err != nil {
			t.Fatalf("record second tunnel hit %d: %v", index, err)
		}
	}

	firstPage, err := store.ListFrpProbeHits(ctx, "tun_1", 1, 100)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	secondPage, err := store.ListFrpProbeHits(ctx, "tun_1", 2, 100)
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if firstPage.Total != 200 || firstPage.HitCount != 205 || len(firstPage.Items) != 100 {
		t.Fatal("unexpected retention page")
	}
	if firstPage.Items[0].Path != "/hit-204" || secondPage.Items[len(secondPage.Items)-1].Path != "/hit-005" {
		t.Fatal("repository did not retain the newest 200 hits")
	}
	secondTunnel, err := store.ListFrpProbeHits(ctx, "tun_2", 1, 100)
	if err != nil {
		t.Fatalf("list second tunnel: %v", err)
	}
	if secondTunnel.Total != 3 || secondTunnel.HitCount != 3 {
		t.Fatal("second tunnel was affected by first tunnel retention")
	}

	if err := store.ClearFrpProbeHits(ctx, "tun_1"); err != nil {
		t.Fatalf("clear first tunnel: %v", err)
	}
	if _, err := store.RecordFrpProbeHit(ctx, probeHit("tun_1", "run_3", 300), DefaultFrpProbeMaxHits); err != nil {
		t.Fatalf("record after clear: %v", err)
	}
	afterClear, err := store.ListFrpProbeHits(ctx, "tun_1", 1, 100)
	if err != nil {
		t.Fatalf("list after clear: %v", err)
	}
	if afterClear.Total != 1 || afterClear.HitCount != 1 {
		t.Fatal("hit count should restart at clear")
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	reopened, err := Open(ctx, databasePath, "../../../../")
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() {
		_ = reopened.Close()
	}()
	persisted, err := reopened.ListFrpProbeHits(ctx, "tun_2", 1, 100)
	if err != nil {
		t.Fatalf("list reopened data: %v", err)
	}
	if persisted.Total != 3 || persisted.HitCount != 3 || persisted.Items[0].Path != "/hit-002" {
		t.Fatal("probe data did not survive reopen")
	}
}

func TestFrpProbeRepositoryDoesNotPersistRuntimeSecrets(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "iquops.db"), "../../../../")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()
	const token = "frp-token-must-not-leak"
	if err := store.UpsertSecretMaterial(ctx, "secret_frp_test", token); err != nil {
		t.Fatalf("store secret: %v", err)
	}
	if _, err := store.RecordFrpProbeHit(ctx, probeHit("tun_1", "run_1", 1), DefaultFrpProbeMaxHits); err != nil {
		t.Fatalf("record hit: %v", err)
	}
	var persisted string
	err = store.db.QueryRowContext(ctx, `SELECT
		id || tunnel_id || run_id || mapping_name || method || host || path || raw_query || remote_addr || user_agent || received_at
		FROM frp_probe_hits WHERE tunnel_id = ? LIMIT 1`, "tun_1").Scan(&persisted)
	if err != nil {
		t.Fatalf("read persisted hit: %v", err)
	}
	if strings.Contains(persisted, token) || strings.Contains(persisted, "secret_frp_test") {
		t.Fatal("probe hit persisted runtime secret material")
	}
}

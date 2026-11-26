package application

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func seedProbeTunnel(t *testing.T, runtime *Runtime, ctx context.Context, tunnelID string, runID string, mappingName string) {
	t.Helper()
	err := runtime.Store.MutateState(ctx, func(state *domain.State) error {
		now := domain.Now()
		mapping := domain.FrpTunnelMapping{
			Name: mappingName, TargetType: domain.FrpTunnelMappingTargetHTTPProbe,
			LocalHost: "127.0.0.1", LocalPort: runtime.Config.FrpProbePort, FrpsProxyPort: 17990,
			PublicPathPrefix: "/", Entrypoint: "web", PublicScheme: "http",
		}
		state.FrpTunnels = append(state.FrpTunnels, domain.FrpTunnel{
			ID: tunnelID, Name: tunnelID, ServerID: "srv_gateway", FrpsBindPort: 7000,
			Mappings: []domain.FrpTunnelMapping{mapping}, CreatedAt: now, UpdatedAt: now,
		})
		state.FrpTunnelRuns = append(state.FrpTunnelRuns, domain.FrpTunnelRun{
			ID: runID, TunnelID: tunnelID, ServerID: "srv_gateway", Status: frpTunnelStatusRunning,
			FrpsBindPort: 7000, Mappings: []domain.FrpTunnelMapping{mapping}, CreatedAt: now, UpdatedAt: now,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed probe tunnel: %v", err)
	}
}

func TestFrpProbeReadinessVerifiesMappingWithoutRecordingHit(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedProbeTunnel(t, runtime, ctx, "tun_probe_check", "tunrun_probe_check", "probe")
	token := domain.NewID("test_token")
	err := store.MutateStateWithSecretMaterials(ctx, map[string]string{"secret_probe_check": token}, func(state *domain.State) error {
		domain.FindFrpTunnelRun(state.FrpTunnelRuns, "tunrun_probe_check").TokenSecretRef = "secret_probe_check"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := NewFrpProbeRecorder("127.0.0.1", 0)
	recorder.SetRecordFunc(runtime.recordFrpProbeRequest)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		request.Header.Set(frpProbeTunnelHeader, "tun_probe_check")
		request.Header.Set(frpProbeRunHeader, "tunrun_probe_check")
		request.Header.Set(frpProbeMappingHeader, "probe")
		recorder.handleProbeRequest(w, request)
	}))
	defer server.Close()
	input := FrpTunnelExecutionInput{Run: domain.FrpTunnelRun{ID: "tunrun_probe_check"}, Token: token}
	if err := verifyPublicFrpProbe(ctx, server.URL+"/probe", input, "probe"); err != nil {
		t.Fatalf("authenticated readiness: %v", err)
	}
	proof := frpProbeProof(token, input.Run.ID, "another-mapping", strconv.FormatInt(time.Now().Unix(), 10)+"_test")
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, server.URL+"/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(frpProbeCheckHeader, proof)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("readiness for the wrong mapping: HTTP %d", response.StatusCode)
	}
	if count, err := store.FrpProbeHitCount(ctx, "tun_probe_check"); count != 0 || err != nil {
		t.Fatalf("readiness recorded %d hits: %v", count, err)
	}
}

func startTestProbeServer(t *testing.T, runtime *Runtime, ctx context.Context) string {
	t.Helper()
	recorder := NewFrpProbeRecorder("127.0.0.1", 0)
	recorder.SetRecordFunc(runtime.recordFrpProbeRequest)
	runtime.FrpProbe = recorder
	if err := recorder.Start(ctx); err != nil {
		t.Fatalf("start probe: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = recorder.Shutdown(shutdownCtx)
	})
	status := recorder.Status()
	if status["enabled"] != true {
		t.Fatal("expected probe enabled")
	}
	return status["localEndpoint"].(string)
}

func requestProbe(t *testing.T, endpoint string, headers map[string]string) map[string]any {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "http://"+endpoint+"/haha?x=1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "iquops-probe-test")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("probe request: %v", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode probe response: %v", err)
	}
	return payload
}

func TestFrpProbeOnlyRecordsValidatedTunnelRunMapping(t *testing.T) {
	runtime, _, ctx := newFrpRuntime(t)
	seedProbeTunnel(t, runtime, ctx, "tun_1", "tunrun_1", "probe")
	endpoint := startTestProbeServer(t, runtime, ctx)

	unscoped := requestProbe(t, endpoint, nil)
	if unscoped["ok"] != true || unscoped["recorded"] != false {
		t.Fatal("unscoped request should be healthy but not recorded")
	}
	spoofed := requestProbe(t, endpoint, map[string]string{
		frpProbeTunnelHeader: "tun_1", frpProbeRunHeader: "tunrun_spoofed", frpProbeMappingHeader: "probe",
	})
	if spoofed["recorded"] != false {
		t.Fatal("spoofed run should not be recorded")
	}
	valid := requestProbe(t, endpoint, map[string]string{
		frpProbeTunnelHeader: "tun_1", frpProbeRunHeader: "tunrun_1", frpProbeMappingHeader: "probe",
	})
	if valid["recorded"] != true || valid["hitCount"] != float64(1) {
		t.Fatal("validated request should be recorded")
	}
	err := runtime.Store.MutateState(ctx, func(state *domain.State) error {
		previous := domain.FindFrpTunnelRun(state.FrpTunnelRuns, "tunrun_1")
		if previous == nil {
			return nil
		}
		now := domain.Now()
		state.FrpTunnelRuns = append(state.FrpTunnelRuns, domain.FrpTunnelRun{
			ID: "tunrun_2", TunnelID: "tun_1", ServerID: previous.ServerID, Status: frpTunnelStatusRunning,
			FrpsBindPort: previous.FrpsBindPort, Mappings: previous.Mappings, CreatedAt: now, UpdatedAt: now,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("append newer run: %v", err)
	}
	stale := requestProbe(t, endpoint, map[string]string{
		frpProbeTunnelHeader: "tun_1", frpProbeRunHeader: "tunrun_1", frpProbeMappingHeader: "probe",
	})
	if stale["recorded"] != false {
		t.Fatal("a stale run must not keep recording after restart")
	}

	page, err := runtime.FrpProbeHits(ctx, "tun_1", 1, 20)
	if err != nil {
		t.Fatalf("list probe hits: %v", err)
	}
	if page.Total != 1 || page.HitCount != 1 || len(page.Items) != 1 {
		t.Fatal("unexpected scoped page")
	}
	hit := page.Items[0]
	if hit.TunnelID != "tun_1" || hit.RunID != "tunrun_1" || hit.MappingName != "probe" || hit.Path != "/haha" || hit.RawQuery != "x=1" {
		t.Fatal("unexpected persisted attribution")
	}
}

func TestFrpProbeHitsAreIsolatedAndClearedPerTunnel(t *testing.T) {
	runtime, _, ctx := newFrpRuntime(t)
	seedProbeTunnel(t, runtime, ctx, "tun_1", "tunrun_1", "probe-a")
	seedProbeTunnel(t, runtime, ctx, "tun_2", "tunrun_2", "probe-b")
	for _, request := range []FrpProbeRequest{
		{TunnelID: "tun_1", RunID: "tunrun_1", MappingName: "probe-a", Method: "GET", Path: "/a", RemoteAddr: "127.0.0.1:1"},
		{TunnelID: "tun_2", RunID: "tunrun_2", MappingName: "probe-b", Method: "GET", Path: "/b", RemoteAddr: "127.0.0.1:2"},
	} {
		result, err := runtime.recordFrpProbeRequest(ctx, request)
		if err != nil || !result.Recorded {
			t.Fatal("record scoped hit")
		}
	}
	if _, err := runtime.ClearFrpProbeHits(ctx, "tun_1"); err != nil {
		t.Fatalf("clear first tunnel: %v", err)
	}
	first, err := runtime.FrpProbeHits(ctx, "tun_1", 1, 20)
	if err != nil {
		t.Fatalf("list first tunnel: %v", err)
	}
	second, err := runtime.FrpProbeHits(ctx, "tun_2", 1, 20)
	if err != nil {
		t.Fatalf("list second tunnel: %v", err)
	}
	if first.Total != 0 || first.HitCount != 0 {
		t.Fatal("first tunnel was not independently cleared")
	}
	if second.Total != 1 || second.HitCount != 1 || second.Items[0].Path != "/b" {
		t.Fatal("clearing first tunnel affected second")
	}
}

func TestFrpProbeRecorderReportsPortConflictWithoutFailingStart(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen occupied port: %v", err)
	}
	defer func() {
		_ = listener.Close()
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	recorder := NewFrpProbeRecorder("127.0.0.1", port)
	if err := recorder.Start(context.Background()); err != nil {
		t.Fatalf("start should be best-effort, got %v", err)
	}
	status := recorder.Status()
	if status["enabled"] == true {
		t.Fatal("expected disabled status on occupied port")
	}
	if status["errorSummary"] == "" {
		t.Fatal("expected error summary on occupied port")
	}
}

package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const (
	frpProbeMaxHits       = sqlite.DefaultFrpProbeMaxHits
	frpProbeTunnelHeader  = "X-IQuOps-Tunnel-ID"
	frpProbeRunHeader     = "X-IQuOps-Tunnel-Run-ID"
	frpProbeMappingHeader = "X-IQuOps-Tunnel-Mapping"
	frpProbeCheckHeader   = "X-IQuOps-Probe-Check"
)

type FrpProbeRequest struct {
	TunnelID    string
	RunID       string
	MappingName string
	Method      string
	Host        string
	Path        string
	RawQuery    string
	RemoteAddr  string
	UserAgent   string
	CheckProof  string
}

type FrpProbeRecordResult struct {
	Recorded      bool
	ReceivedAt    string
	HitCount      int
	CheckResponse string
}

type FrpProbeRecordFunc func(context.Context, FrpProbeRequest) (FrpProbeRecordResult, error)

type FrpProbeRecorder struct {
	mu             sync.Mutex
	listenHost     string
	configuredPort int
	listenPort     int
	startedAt      string
	errorSummary   string
	server         *http.Server
	record         FrpProbeRecordFunc
}

func NewFrpProbeRecorder(host string, port int) *FrpProbeRecorder {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "127.0.0.1"
	}
	return &FrpProbeRecorder{listenHost: host, configuredPort: port, listenPort: port}
}

func (p *FrpProbeRecorder) SetRecordFunc(record FrpProbeRecordFunc) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.record = record
	p.mu.Unlock()
}

func (p *FrpProbeRecorder) Start(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.server != nil {
		p.mu.Unlock()
		return nil
	}
	host := p.listenHost
	port := p.configuredPort
	p.mu.Unlock()
	if !isLoopbackProbeHost(host) {
		p.markStartFailure("FRP_PROBE_HOST_MUST_BE_LOOPBACK")
		return nil
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		p.markStartFailure(err.Error())
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleProbeRequest)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	actualPort := port
	if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
		actualPort = tcpAddr.Port
	}
	p.mu.Lock()
	p.server = server
	p.listenPort = actualPort
	p.startedAt = domain.Now()
	p.errorSummary = ""
	p.mu.Unlock()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.markStartFailure(err.Error())
		}
	}()
	return nil
}

func (p *FrpProbeRecorder) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	server := p.server
	p.server = nil
	p.startedAt = ""
	p.mu.Unlock()
	if server == nil {
		return nil
	}
	return server.Shutdown(ctx)
}

func (p *FrpProbeRecorder) Status() domain.Object {
	if p == nil {
		return domain.Object{
			"enabled": false, "listenHost": "127.0.0.1", "listenPort": 17990,
			"localEndpoint": "127.0.0.1:17990", "errorSummary": "FRP_PROBE_NOT_INITIALIZED",
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	status := domain.Object{
		"enabled": p.server != nil && p.errorSummary == "", "listenHost": p.listenHost,
		"listenPort": p.listenPort, "localEndpoint": net.JoinHostPort(p.listenHost, strconv.Itoa(p.listenPort)),
	}
	if p.startedAt != "" {
		status["startedAt"] = p.startedAt
	}
	if p.errorSummary != "" {
		status["errorSummary"] = p.errorSummary
	}
	return status
}

func (p *FrpProbeRecorder) handleProbeRequest(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		w.Header().Set("allow", strings.Join([]string{http.MethodGet, http.MethodHead}, ", "))
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p.mu.Lock()
	record := p.record
	p.mu.Unlock()
	result := FrpProbeRecordResult{ReceivedAt: domain.Now()}
	if record != nil {
		recorded, err := record(request.Context(), FrpProbeRequest{
			TunnelID: strings.TrimSpace(request.Header.Get(frpProbeTunnelHeader)), RunID: strings.TrimSpace(request.Header.Get(frpProbeRunHeader)),
			MappingName: strings.TrimSpace(request.Header.Get(frpProbeMappingHeader)), Method: request.Method, Host: request.Host,
			Path: request.URL.Path, RawQuery: request.URL.RawQuery, RemoteAddr: request.RemoteAddr, UserAgent: request.UserAgent(),
			CheckProof: request.Header.Get(frpProbeCheckHeader),
		})
		if err == nil {
			result = recorded
		}
	}
	if request.Header.Get(frpProbeCheckHeader) != "" {
		if result.CheckResponse == "" {
			http.NotFound(w, request)
			return
		}
		w.Header().Set(frpProbeCheckHeader, result.CheckResponse)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body := domain.Object{
		"ok": true, "path": request.URL.Path, "receivedAt": result.ReceivedAt, "recorded": result.Recorded,
	}
	if result.Recorded {
		body["hitCount"] = result.HitCount
	}
	if strings.Contains(request.Header.Get("accept"), "application/json") {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	if request.Method == http.MethodHead {
		return
	}
	_, _ = fmt.Fprintf(w, "ok path=%s receivedAt=%s recorded=%t\n", request.URL.Path, result.ReceivedAt, result.Recorded)
}

func (p *FrpProbeRecorder) markStartFailure(summary string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.server = nil
	p.startedAt = ""
	p.errorSummary = summary
	if p.listenPort == 0 {
		p.listenPort = p.configuredPort
	}
}

func (r *Runtime) StartFrpProbeServer(ctx context.Context) error {
	if r.FrpProbe == nil {
		r.FrpProbe = NewFrpProbeRecorder(r.Config.FrpProbeHost, r.Config.FrpProbePort)
		r.FrpProbe.SetRecordFunc(r.recordFrpProbeRequest)
	}
	return r.FrpProbe.Start(ctx)
}

func (r *Runtime) ShutdownFrpProbeServer(ctx context.Context) error {
	if r.FrpProbe == nil {
		return nil
	}
	return r.FrpProbe.Shutdown(ctx)
}

func (r *Runtime) FrpProbeStatus(ctx context.Context) (domain.Object, error) {
	_ = ctx
	return r.FrpProbe.Status(), nil
}

func (r *Runtime) FrpProbeHits(ctx context.Context, tunnelID string, page int, pageSize int) (sqlite.FrpProbeHitPage, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return sqlite.FrpProbeHitPage{}, err
	}
	if domain.FindFrpTunnel(state.FrpTunnels, tunnelID) == nil {
		return sqlite.FrpProbeHitPage{}, errors.New("FRP_TUNNEL_NOT_FOUND")
	}
	return r.Store.ListFrpProbeHits(ctx, tunnelID, page, pageSize)
}

func (r *Runtime) ClearFrpProbeHits(ctx context.Context, tunnelID string) (domain.Object, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	if domain.FindFrpTunnel(state.FrpTunnels, tunnelID) == nil {
		return nil, errors.New("FRP_TUNNEL_NOT_FOUND")
	}
	if err := r.Store.ClearFrpProbeHits(ctx, tunnelID); err != nil {
		return nil, err
	}
	return domain.Object{"ok": true, "tunnelId": tunnelID, "clearedAt": domain.Now()}, nil
}

func (r *Runtime) recordFrpProbeRequest(ctx context.Context, request FrpProbeRequest) (FrpProbeRecordResult, error) {
	result := FrpProbeRecordResult{ReceivedAt: domain.Now()}
	if request.TunnelID == "" || request.RunID == "" || request.MappingName == "" {
		return result, nil
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return result, err
	}
	if domain.FindFrpTunnel(state.FrpTunnels, request.TunnelID) == nil {
		return result, nil
	}
	run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, request.RunID)
	if run == nil || run.TunnelID != request.TunnelID || (run.Status != frpTunnelStatusStarting && run.Status != frpTunnelStatusRunning && run.Status != frpTunnelStatusStopping) {
		return result, nil
	}
	latestRun := domain.LatestFrpTunnelRun(state.FrpTunnelRuns, request.TunnelID)
	if latestRun == nil || latestRun.ID != run.ID {
		return result, nil
	}
	validMapping := false
	for _, mapping := range run.Mappings {
		if mapping.Name == request.MappingName && mapping.TargetType == domain.FrpTunnelMappingTargetHTTPProbe {
			validMapping = true
			break
		}
	}
	if !validMapping {
		return result, nil
	}
	if request.CheckProof != "" {
		token, err := r.Store.GetSecretMaterial(ctx, run.TokenSecretRef)
		if err == nil && token != "" {
			result.CheckResponse = frpProbeCheckResponse(request.CheckProof, token, run.ID, request.MappingName)
		}
		return result, nil
	}
	hit := domain.FrpProbeHit{
		ID: domain.NewID("probehit"), TunnelID: request.TunnelID, RunID: request.RunID, MappingName: request.MappingName,
		Method: strings.ToUpper(strings.TrimSpace(request.Method)), Host: strings.TrimSpace(request.Host), Path: strings.TrimSpace(request.Path),
		RawQuery: strings.TrimSpace(request.RawQuery), RemoteAddr: strings.TrimSpace(request.RemoteAddr), UserAgent: strings.TrimSpace(request.UserAgent), ReceivedAt: result.ReceivedAt,
	}
	if hit.Method == "" {
		hit.Method = http.MethodGet
	}
	if hit.Path == "" {
		hit.Path = "/"
	}
	hitCount, err := r.Store.RecordFrpProbeHit(ctx, hit, frpProbeMaxHits)
	if err != nil {
		return result, err
	}
	result.Recorded = true
	result.HitCount = hitCount
	return result, nil
}

func isLoopbackProbeHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

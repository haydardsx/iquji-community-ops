package application

import (
	"context"
	"sync"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

type Runtime struct {
	Config                     platform.Config
	Store                      *sqlite.Store
	Cache                      *platform.Cache
	Runner                     platform.CommandRunner
	FrpExecutor                FrpTunnelExecutor
	FrpProbe                   *FrpProbeRecorder
	FrpsRuntimeExecutor        FrpsRuntimeExecutor
	ServerRuntimeExecutor      ServerRuntimeExecutor
	RuntimeResourceExecutor    RuntimeResourceExecutor
	ApplicationRuntimeExecutor ApplicationRuntimeExecutor
	RouteExecutor              RouteRuntimeExecutor
	CertificateVerifier        CertificateVerifier
	JenkinsReleaseExecutor     JenkinsReleaseExecutor
	Version                    string
	cleanupMu                  sync.Mutex
}

func NewRuntime(config platform.Config, store *sqlite.Store) *Runtime {
	serverRuntimeExecutor := NewSystemServerRuntimeExecutor()
	runtime := &Runtime{
		Config:                     config,
		Store:                      store,
		Cache:                      platform.NewCache(),
		Runner:                     platform.CommandRunner{Timeout: 15 * time.Second},
		FrpExecutor:                NewSystemFrpTunnelExecutor(),
		FrpProbe:                   NewFrpProbeRecorder(config.FrpProbeHost, config.FrpProbePort),
		FrpsRuntimeExecutor:        serverRuntimeExecutor,
		ServerRuntimeExecutor:      serverRuntimeExecutor,
		RuntimeResourceExecutor:    NewSystemRuntimeResourceExecutor(),
		ApplicationRuntimeExecutor: NewSystemApplicationRuntimeExecutor(),
		RouteExecutor:              NewSystemRouteRuntimeExecutor(),
		CertificateVerifier:        NewSystemCertificateVerifier(),
		JenkinsReleaseExecutor:     NewSystemJenkinsReleaseExecutor(),
		Version:                    readVersion(config.RepositoryRoot),
	}
	runtime.FrpProbe.SetRecordFunc(runtime.recordFrpProbeRequest)
	return runtime
}

func (r *Runtime) LocalConfig(ctx context.Context) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"controlPlane": r.Config.ControlPlane(r.Version),
		"generatedAt":  domain.Now(),
	}, nil
}

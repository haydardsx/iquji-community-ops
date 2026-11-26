package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

type testAPI struct {
	server  *httptest.Server
	store   *sqlite.Store
	runtime *application.Runtime
}

// 每次测试进程生成独立材料，仓库不保存可复用私钥。
var validHTTPTestSSHPrivateKey = func() string {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic("生成测试 SSH 私钥失败：" + err.Error())
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		panic("编码测试 SSH 私钥失败：" + err.Error())
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}()

type fakeHTTPServerRuntimeExecutor struct{}

type fakeHTTPFrpExecutor struct{}

func (f *fakeHTTPFrpExecutor) StartFrpTunnel(context.Context, application.FrpTunnelExecutionInput) error {
	return nil
}

func (f *fakeHTTPFrpExecutor) StopFrpTunnel(context.Context, application.FrpTunnelExecutionInput) error {
	return nil
}

func (f *fakeHTTPFrpExecutor) CleanupFrpTunnel(context.Context, application.FrpTunnelExecutionInput) error {
	return nil
}

func (f *fakeHTTPServerRuntimeExecutor) ProbeServerRuntime(context.Context, application.ServerRuntimeExecutionInput) (domain.Object, error) {
	return domain.Object{
		"checks": []domain.Object{
			{"name": "ssh", "status": "success", "summary": "SSH reachable"},
			{"name": "docker", "status": "success", "summary": "Docker test"},
			{"name": "traefik", "status": "success", "summary": "Traefik gateway available"},
		},
		"metrics": domain.Object{
			"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "swarmNodeId": "node_test",
			"traefik": true, "traefikStatus": "running", "frps": true, "frpsStatus": "installed", "agent": false,
		},
		"remote": domain.Object{},
	}, nil
}

func (f *fakeHTTPServerRuntimeExecutor) InstallDockerEngine(context.Context, application.ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeHTTPServerRuntimeExecutor) UninstallDockerEngine(context.Context, application.ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeHTTPServerRuntimeExecutor) InstallDockerSwarm(context.Context, application.ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeHTTPServerRuntimeExecutor) UninstallDockerSwarm(context.Context, application.ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeHTTPServerRuntimeExecutor) ReconfigureDockerSwarm(context.Context, application.ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, nil
}

func (f *fakeHTTPServerRuntimeExecutor) RebuildDockerSwarm(context.Context, application.ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("未配置测试执行器")
}

func (f *fakeHTTPServerRuntimeExecutor) InstallJenkins(context.Context, application.ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeHTTPServerRuntimeExecutor) UninstallJenkins(context.Context, application.ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeHTTPServerRuntimeExecutor) InstallMonitoringAgent(context.Context, application.ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeHTTPServerRuntimeExecutor) UninstallMonitoringAgent(context.Context, application.ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("not implemented")
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()
	ctx := context.Background()
	tempDir := t.TempDir()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	config := platform.Resolve(platform.RoleAPI)
	config.RepositoryRoot = root
	config.StateDir = tempDir
	config.DatabasePath = filepath.Join(tempDir, "state.sqlite")
	config.FrpProbeHost = "127.0.0.1"
	config.FrpProbePort = 17990
	store, err := sqlite.Open(ctx, config.DatabasePath, config.RepositoryRoot)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	runtime := application.NewRuntime(config, store)
	runtime.ServerRuntimeExecutor = &fakeHTTPServerRuntimeExecutor{}
	server := httptest.NewServer(New(runtime).Handler())
	t.Cleanup(func() {
		server.Close()
		_ = store.Close()
	})
	return &testAPI{server: server, store: store, runtime: runtime}
}

func TestManagedReleaseEndpointsRejectUnknownNestedFieldsAndTrailingJSON(t *testing.T) {
	api := newTestAPI(t)
	tests := []struct {
		path string
		body string
	}{
		{
			path: "/projects/applications/app_one/deployment-config",
			body: `{"buildServerId":"srv_build","deliveryConfig":{"executor":"jenkins","buildServerId":"nested","pipeline":{"folder":"iquops","jobName":"iquops-standard-swarm","jobTemplate":"iquops-standard-swarm","triggerMode":"ops_manual"},"registrySync":{"enabled":false}}}`,
		},
		{
			path: "/projects/applications/app_one/deployment-config",
			body: `{"buildServerId":"srv_build","deliveryConfig":{"executor":"jenkins","pipeline":{"folder":"iquops","jobName":"iquops-standard-swarm","jobTemplate":"iquops-standard-swarm","triggerMode":"ops_manual","jenkinsUrl":"https://external.example"},"registrySync":{"enabled":false}}}`,
		},
	}
	for _, item := range tests {
		request, err := http.NewRequest(http.MethodPut, api.server.URL+item.path, bytes.NewBufferString(item.body))
		if err != nil {
			t.Fatalf("create strict request: %v", err)
		}
		request.Header.Set("content-type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("strict request: %v", err)
		}
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("unknown managed Jenkins field status = %d", response.StatusCode)
		}
		_ = response.Body.Close()
	}

	request, err := http.NewRequest(http.MethodPost, api.server.URL+"/projects/applications/app_one/release-plans/preview", bytes.NewBufferString(`{"ref":"main","refType":"branch"}{"ref":"second"}`))
	if err != nil {
		t.Fatalf("create trailing JSON request: %v", err)
	}
	request.Header.Set("content-type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("trailing JSON request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("trailing JSON status = %d", response.StatusCode)
	}
}

func TestJenkinsCallbackRejectsUnknownFieldsAndMissingToken(t *testing.T) {
	api := newTestAPI(t)
	invalid := api.request(t, http.MethodPost, "/platform/jenkins/release-artifacts", map[string]any{"callbackTunnelUrl": "http://untrusted"})
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知回调字段应拒绝: %d", invalid.StatusCode)
	}
	_ = invalid.Body.Close()
	response := api.request(t, http.MethodPost, "/platform/jenkins/release-artifacts", map[string]any{"runId": "run_one", "releasePlanId": "release_one", "applicationId": "app_one", "status": "succeeded", "summary": "完成", "outputValues": map[string]string{"PUBLIC_ENDPOINT": "https://app.example.invalid"}, "jenkinsRun": map[string]any{"runId": "run_one", "status": "succeeded"}})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无用途凭据的回调应拒绝: %d", response.StatusCode)
	}
	_ = response.Body.Close()
}
func TestJenkinsRunMaterialsRequireRunToken(t *testing.T) {
	api := newTestAPI(t)
	response := api.request(t, http.MethodGet, "/platform/jenkins/runs/run_one/materials", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无凭据不能获取构建材料: %d", response.StatusCode)
	}
	_ = response.Body.Close()
}

func TestServerDeleteReturnsStructuredBlockedImpact(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_build", "name": "Build"}}
		state.Applications = []domain.Object{{"id": "app_one", "name": "Example"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"id": "config_one", "applicationId": "app_one", "buildServerId": "srv_build"}}
		return nil
	}); err != nil {
		t.Fatalf("seed delete impact: %v", err)
	}
	response := api.request(t, http.MethodDelete, "/servers/srv_build", nil)
	if response.StatusCode != http.StatusConflict {
		defer func() { _ = response.Body.Close() }()
		t.Fatalf("blocked server delete status = %d", response.StatusCode)
	}
	payload := decodeTestJSON[map[string]any](t, response)
	if payload["errorCode"] != "SERVER_DELETE_BLOCKED" {
		t.Fatal("blocked server returned the wrong error")
	}
	details := objectMap(payload["details"])
	if len(objectMap(details["impact"])) == 0 {
		t.Fatal("server delete impact details missing")
	}
}

func openHTTPTestTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen local tcp: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func (api *testAPI) request(t *testing.T, method string, path string, payload any) *http.Response {
	t.Helper()
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	req, err := http.NewRequest(method, api.server.URL+path, &body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}
	return response
}

func decodeTestJSON[T any](t *testing.T, response *http.Response) T {
	t.Helper()
	defer func() {
		_ = response.Body.Close()
	}()
	var value T
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return value
}

func TestProgressQueryRoutesAreRemoved(t *testing.T) {
	api := newTestAPI(t)
	for _, path := range []string{
		"/operations/tasks",
		"/operations/tasks/contextual",
		"/logs/task-steps?taskId=operation_1",
	} {
		response := api.request(t, http.MethodGet, path, nil)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d", path, response.StatusCode)
		}
		_ = response.Body.Close()
	}
	invalidStatus := api.request(t, http.MethodGet, "/operations/runs?status=unknown", nil)
	if invalidStatus.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid operation status = %d", invalidStatus.StatusCode)
	}
	_ = invalidStatus.Body.Close()
}

func TestBackupRestoreHTTPContractsAreStrictAndLegacyRoutesStayRemoved(t *testing.T) {
	api := newTestAPI(t)
	unknownBackupField := api.request(t, http.MethodPost, "/backup-restore/backups/preview", map[string]any{
		"options": map[string]any{
			"requestedScopes":      []string{"projects"},
			"includeSensitiveData": false,
			"includeLogsAndAudit":  false,
			"bundleType":           "project-bundle",
		},
	})
	if unknownBackupField.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown backup field status = %d", unknownBackupField.StatusCode)
	}
	_ = unknownBackupField.Body.Close()

	legacyRoute := api.request(t, http.MethodPost, "/import-export/project-bundle/export", map[string]any{})
	if legacyRoute.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy import-export route must stay removed, status = %d", legacyRoute.StatusCode)
	}
	_ = legacyRoute.Body.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "legacy.json")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := file.Write([]byte(`{"bundleType":"project-bundle","entities":{"projects":[]}}`)); err != nil {
		t.Fatalf("write legacy bundle: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, api.server.URL+"/backup-restore/restores/uploads", &body)
	if err != nil {
		t.Fatalf("create legacy upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("upload legacy bundle: %v", err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("legacy upload status = %d", response.StatusCode)
	}
	errorResponse := decodeTestJSON[map[string]any](t, response)
	if errorResponse["errorCode"] != "LEGACY_BUNDLE_UNSUPPORTED" {
		t.Fatal("legacy bundle returned the wrong error")
	}
}

func TestReleaseConfirmationPolicyIgnoresLegacyFlagAndAcceptsQueuedRebuild(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "app_release", "name": "发布测试"}}
		state.Servers = []domain.Object{{"id": "srv_release", "health": domain.Object{"metrics": domain.Object{"ssh": true, "docker": true, "jenkins": true, "jenkinsBundleVersion": "v12-jenkins-2.568.1", "swarm": true, "swarmRole": "manager", "swarmVerificationStatus": "verified", "swarmClusterId": "cluster_one", "traefik": true}}}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app_release", "buildServerId": "srv_release", "deploymentManagerServerId": "srv_release", "sourceConfig": domain.Object{"provider": "git", "repositoryUrl": "https://example.com/demo.git", "ref": "main", "refType": "branch", "sourcePath": ".", "jenkinsfilePath": "Jenkinsfile"}, "swarmRuntime": domain.Object{"services": []domain.Object{{"serverId": "srv_release", "slotKey": "web", "serviceName": "app-web"}}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	path := "/projects/applications/app_release/rebuild"
	legacy := api.request(t, http.MethodPost, path, map[string]any{"requiresConfirmation": false})
	if legacy.StatusCode != http.StatusBadRequest {
		t.Fatalf("不得绕过确认: %d", legacy.StatusCode)
	}
	_ = legacy.Body.Close()
	firstResponse := api.request(t, http.MethodPost, path, map[string]any{})
	first := decodeTestJSON[map[string]any](t, firstResponse)
	if firstResponse.StatusCode != http.StatusOK || first["status"] != "queued" {
		t.Fatal("创建构建操作失败")
	}
	state, _ := api.store.LoadState(context.Background())
	if len(state.Tasks) != 1 || state.Tasks[0].Status != "pending_confirmation" {
		t.Fatal("构建必须等待原位确认")
	}
	second := api.request(t, http.MethodPost, path, map[string]any{})
	if second.StatusCode != http.StatusOK {
		t.Fatalf("相同应用后续发布必须接受排队: %d", second.StatusCode)
	}
	_ = second.Body.Close()
}

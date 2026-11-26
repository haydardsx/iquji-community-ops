package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestAuthenticatedParityRoutesReturnStatefulResponses(t *testing.T) {
	api := newTestAPI(t)

	sourceResponse := api.request(t, http.MethodPost, "/secrets/assets", map[string]any{
		"name":        "GitHub token",
		"assetType":   "git_token",
		"secretValue": "sample-git-value",
	})
	if sourceResponse.StatusCode != http.StatusOK {
		t.Fatalf("source credential status = %d", sourceResponse.StatusCode)
	}
	sourceBody := decodeTestJSON[map[string]map[string]any](t, sourceResponse)
	if sourceBody["asset"]["assetType"] != "git_token" || sourceBody["asset"]["purpose"] != "git_auth" {
		t.Fatal("unexpected source credential")
	}
	if sourceBody["operation"] == nil {
		t.Fatal("source credential should return an operation")
	}

	sourceOperationID := sourceBody["operation"]["id"].(string)
	confirmedSource := api.request(t, http.MethodPost, "/operations/runs/"+sourceOperationID+"/confirm", nil)
	if confirmedSource.StatusCode != http.StatusOK {
		t.Fatalf("Git 凭据确认失败：%d", confirmedSource.StatusCode)
	}
	_ = confirmedSource.Body.Close()
	assertSecretAssetWorkerSuccess(t, api, sourceOperationID)

	secretResponse := api.request(t, http.MethodPost, "/secrets/assets", map[string]any{
		"name":        "Deploy SSH",
		"assetType":   "ssh_key",
		"purpose":     "deployment_server",
		"secretValue": validHTTPTestSSHPrivateKey,
	})
	if secretResponse.StatusCode != http.StatusOK {
		t.Fatalf("secret create status = %d", secretResponse.StatusCode)
	}
	secretBody := decodeTestJSON[map[string]any](t, secretResponse)
	asset := secretBody["asset"].(map[string]any)
	secretOperationID := secretBody["operation"].(map[string]any)["id"].(string)
	confirmedSecret := api.request(t, http.MethodPost, "/operations/runs/"+secretOperationID+"/confirm", nil)
	if confirmedSecret.StatusCode != http.StatusOK {
		t.Fatalf("凭据确认失败：%d", confirmedSecret.StatusCode)
	}
	_ = confirmedSecret.Body.Close()
	assertSecretAssetWorkerSuccess(t, api, secretOperationID)
	if secretBody["operation"] == nil || asset["id"] == "" {
		t.Fatal("secret response missing asset/operation")
	}

	previewResponse := api.request(t, http.MethodGet, "/operations/delete-preview?targetKind=secret_asset&targetId="+asset["id"].(string), nil)
	if previewResponse.StatusCode != http.StatusOK {
		t.Fatalf("delete preview status = %d", previewResponse.StatusCode)
	}
	preview := decodeTestJSON[map[string]any](t, previewResponse)
	deleteProcess := preview["process"].(map[string]any)
	decision := deleteProcess["decision"].(map[string]any)
	if decision["allowed"] != true {
		t.Fatal("delete preview should allow secret asset delete")
	}

	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Ingresses = append(state.Ingresses, domain.Object{"id": "ingress_certificate", "serverIds": []string{"srv_local"}})
		state.Resolvers = append(state.Resolvers, domain.Object{"id": "resolver_certificate", "ingressId": "ingress_certificate"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	certResponse := api.request(t, http.MethodPost, "/secrets/certificates", map[string]any{
		"name": "受管证书", "domains": []string{"example.com"},
		"issuer": "lets_encrypt", "challengeType": "http-01", "resolverId": "resolver_certificate",
	})
	if certResponse.StatusCode != http.StatusOK {
		t.Fatalf("certificate status = %d", certResponse.StatusCode)
	}
	certBody := decodeTestJSON[map[string]map[string]any](t, certResponse)
	if certBody["certificate"]["status"] != "pending" || certBody["task"] != nil {
		t.Fatal("证书资产必须等待路由真实验证")
	}

	registerResponse := api.request(t, http.MethodPost, "/monitoring/agents/register", map[string]any{
		"serverId":        "srv_local",
		"version":         "test",
		"protocolVersion": 1,
		"capabilities":    []string{"metrics"},
	})
	if registerResponse.StatusCode != http.StatusOK {
		t.Fatalf("agent register status = %d", registerResponse.StatusCode)
	}
	agentBody := decodeTestJSON[map[string]map[string]any](t, registerResponse)
	if agentBody["agent"]["status"] != "online" {
		t.Fatal("agent should be online")
	}

	backupPreviewResponse := api.request(t, http.MethodPost, "/backup-restore/backups/preview", map[string]any{
		"options": map[string]any{
			"requestedScopes":      []string{"domains_certificates", "secret_assets"},
			"includeSensitiveData": false,
			"includeLogsAndAudit":  false,
		},
	})
	if backupPreviewResponse.StatusCode != http.StatusOK {
		t.Fatalf("backup preview status = %d", backupPreviewResponse.StatusCode)
	}
	backupPreview := decodeTestJSON[domain.BackupPreviewResponse](t, backupPreviewResponse)
	if backupPreview.Manifest.RecordCounts["secretAssets"] < 2 || backupPreview.Manifest.RecordCounts["certificates"] < 1 {
		t.Fatal("backup preview did not reflect created resources")
	}
}

func TestFrpTunnelHTTPRoutesCreateTasksWithoutLeakingSecrets(t *testing.T) {
	api := newTestAPI(t)
	api.runtime.FrpExecutor = &fakeHTTPFrpExecutor{}
	localPort := openHTTPTestTCPPort(t)

	secretResponse := api.request(t, http.MethodPost, "/secrets/assets", map[string]any{
		"name":        "Tunnel SSH",
		"assetType":   "ssh_key",
		"secretValue": validHTTPTestSSHPrivateKey,
	})
	if secretResponse.StatusCode != http.StatusOK {
		t.Fatalf("secret status = %d", secretResponse.StatusCode)
	}
	secretBody := decodeTestJSON[map[string]any](t, secretResponse)
	asset := secretBody["asset"].(map[string]any)
	secretOperationID := secretBody["operation"].(map[string]any)["id"].(string)
	confirmedSecret := api.request(t, http.MethodPost, "/operations/runs/"+secretOperationID+"/confirm", nil)
	if confirmedSecret.StatusCode != http.StatusOK {
		t.Fatalf("凭据确认失败：%d", confirmedSecret.StatusCode)
	}
	_ = confirmedSecret.Body.Close()
	assertSecretAssetWorkerSuccess(t, api, secretOperationID)

	serverResponse := api.request(t, http.MethodPost, "/servers", map[string]any{
		"name":           "Gateway",
		"publicHost":     "203.0.113.10",
		"environment":    "staging",
		"secretAssetIds": []string{asset["id"].(string)},
	})
	if serverResponse.StatusCode != http.StatusOK {
		t.Fatalf("server status = %d", serverResponse.StatusCode)
	}
	server := decodeTestJSON[map[string]any](t, serverResponse)
	serverID := server["id"].(string)

	probeResponse := api.request(t, http.MethodPost, "/servers/"+serverID+"/runtime/probe", nil)
	if probeResponse.StatusCode != http.StatusOK {
		t.Fatalf("runtime probe status = %d", probeResponse.StatusCode)
	}
	probe := decodeTestJSON[map[string]any](t, probeResponse)
	probeProcess := probe["process"].(map[string]any)
	if len(probeProcess["checks"].([]any)) == 0 {
		t.Fatal("runtime probe should include checks")
	}

	request := map[string]any{
		"name":         "Local API",
		"serverId":     serverID,
		"frpsBindPort": 7000,
		"mappings": []map[string]any{
			{
				"targetType":    "local_service",
				"name":          "local-api",
				"localHost":     "127.0.0.1",
				"localPort":     localPort,
				"frpsProxyPort": 18081,
			},
		},
	}
	previewResponse := api.request(t, http.MethodPost, "/servers/tunnels/preview", request)
	if previewResponse.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d", previewResponse.StatusCode)
	}
	preview := decodeTestJSON[map[string]any](t, previewResponse)
	previewProcess := preview["process"].(map[string]any)
	previewDecision := previewProcess["decision"].(map[string]any)
	if previewDecision["allowed"] != true {
		t.Fatal("preview should allow tunnel")
	}
	endpoints := preview["tunnelPreview"].(map[string]any)["endpoints"].([]any)
	if len(endpoints) != 1 || endpoints[0] != "http://203.0.113.10:18081" {
		t.Fatal("无域名映射必须展示 FRPS 代理端口")
	}
	request["sessionMode"] = "temporary"
	strictResponse := api.request(t, http.MethodPost, "/servers/tunnels/preview", request)
	if strictResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("strict tunnel input should reject unknown fields, status=%d", strictResponse.StatusCode)
	}
	_ = strictResponse.Body.Close()
	delete(request, "sessionMode")

	createResponse := api.request(t, http.MethodPost, "/servers/tunnels", request)
	if createResponse.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d", createResponse.StatusCode)
	}
	created := decodeTestJSON[map[string]any](t, createResponse)
	tunnel := created["tunnel"].(map[string]any)
	run := created["run"].(map[string]any)
	if tunnel["tokenSecretRef"] != nil || run["tokenSecretRef"] != nil || run["containerName"] != nil {
		t.Fatal("create response leaked runtime secrets")
	}
	task := created["task"].(map[string]any)
	if task["status"] != "queued" {
		t.Fatal("start operation should use the queued status")
	}

	listResponse := api.request(t, http.MethodGet, "/servers/tunnels", nil)
	if listResponse.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", listResponse.StatusCode)
	}
	list := decodeTestJSON[map[string]any](t, listResponse)
	if len(list["items"].([]any)) != 1 {
		t.Fatal("list should include tunnel")
	}

	tunnelID := tunnel["id"].(string)
	firstRunID := run["id"].(string)
	getResponse := api.request(t, http.MethodGet, "/servers/tunnels/"+tunnelID, nil)
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", getResponse.StatusCode)
	}
	got := decodeTestJSON[map[string]any](t, getResponse)
	if got["tokenSecretRef"] != nil {
		t.Fatal("get leaked token secret ref")
	}

	taskID := task["id"].(string)
	confirmResponse := api.request(t, http.MethodPost, "/operations/runs/"+taskID+"/confirm", nil)
	if confirmResponse.StatusCode != http.StatusOK {
		t.Fatalf("confirm status = %d", confirmResponse.StatusCode)
	}
	_ = decodeTestJSON[map[string]any](t, confirmResponse)
	if _, err := api.runtime.RunWorkerOnce(context.Background()); err != nil {
		t.Fatalf("run start worker: %v", err)
	}

	stopResponse := api.request(t, http.MethodPost, "/servers/tunnels/"+tunnelID+"/stop", nil)
	if stopResponse.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d", stopResponse.StatusCode)
	}
	stopTask := decodeTestJSON[map[string]any](t, stopResponse)
	if stopTask["action"] != "stop_frp_tunnel" {
		t.Fatal("unexpected stop task")
	}
	stopTarget := stopTask["target"].(map[string]any)
	if stopTarget["kind"] != "frp_tunnel_run" || stopTarget["id"] != firstRunID {
		t.Fatal("stop task must target active run")
	}
	if stopTask["status"] != "queued" {
		t.Fatal("stop operation must use the queued status")
	}
	stopTaskID := stopTask["id"].(string)
	stopConfirmResponse := api.request(t, http.MethodPost, "/operations/runs/"+stopTaskID+"/confirm", nil)
	if stopConfirmResponse.StatusCode != http.StatusOK {
		t.Fatalf("confirm stop status = %d", stopConfirmResponse.StatusCode)
	}
	_ = decodeTestJSON[map[string]any](t, stopConfirmResponse)
	if _, err := api.runtime.RunWorkerOnce(context.Background()); err != nil {
		t.Fatalf("run stop worker: %v", err)
	}

	request["name"] = "Renamed API"
	updateResponse := api.request(t, http.MethodPut, "/servers/tunnels/"+tunnelID, request)
	if updateResponse.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d", updateResponse.StatusCode)
	}
	updated := decodeTestJSON[map[string]any](t, updateResponse)
	if updated["name"] != "Renamed API" || updated["status"] != "stopped" {
		t.Fatal("unexpected update response")
	}

	restartResponse := api.request(t, http.MethodPost, "/servers/tunnels/"+tunnelID+"/start", nil)
	if restartResponse.StatusCode != http.StatusOK {
		t.Fatalf("restart status = %d", restartResponse.StatusCode)
	}
	restarted := decodeTestJSON[map[string]any](t, restartResponse)
	secondRun := restarted["run"].(map[string]any)
	if secondRun["id"] == firstRunID || restarted["task"].(map[string]any)["status"] != "queued" {
		t.Fatal("restart must create a new confirmed run")
	}
	runsResponse := api.request(t, http.MethodGet, "/servers/tunnels/"+tunnelID+"/runs?page=1&pageSize=20", nil)
	if runsResponse.StatusCode != http.StatusOK {
		t.Fatalf("runs status = %d", runsResponse.StatusCode)
	}
	runs := decodeTestJSON[map[string]any](t, runsResponse)
	if runs["total"] != float64(2) || len(runs["items"].([]any)) != 2 {
		t.Fatal("unexpected run history")
	}
	restartTaskID := restarted["task"].(map[string]any)["id"].(string)
	cancelResponse := api.request(t, http.MethodPost, "/operations/runs/"+restartTaskID+"/cancel", nil)
	if cancelResponse.StatusCode != http.StatusOK {
		t.Fatalf("cancel restart status = %d", cancelResponse.StatusCode)
	}
	_ = decodeTestJSON[map[string]any](t, cancelResponse)
	deleteResponse := api.request(t, http.MethodDelete, "/servers/tunnels/"+tunnelID, nil)
	if deleteResponse.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", deleteResponse.StatusCode)
	}
	deleteTask := decodeTestJSON[map[string]any](t, deleteResponse)
	if deleteTask["action"] != "delete_frp_tunnel" || deleteTask["status"] != "queued" {
		t.Fatal("delete operation must use the queued status")
	}

	cleanupResponse := api.request(t, http.MethodPost, "/servers/tunnels/orphan-cleanup", nil)
	if cleanupResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("cleanup status = %d", cleanupResponse.StatusCode)
	}
}

func TestFrpProbeHTTPRoutesExposeGlobalHealthAndScopedPersistentHits(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()
	now := domain.Now()
	err := api.store.MutateState(ctx, func(state *domain.State) error {
		for _, id := range []string{"tun_1", "tun_2"} {
			state.FrpTunnels = append(state.FrpTunnels, domain.FrpTunnel{
				ID: id, Name: id, ServerID: "srv_1", FrpsBindPort: 7000,
				Mappings: []domain.FrpTunnelMapping{{
					Name: "probe", TargetType: domain.FrpTunnelMappingTargetHTTPProbe, LocalHost: "127.0.0.1",
					LocalPort: 17990, FrpsProxyPort: 17990, PublicPathPrefix: "/", Entrypoint: "web", PublicScheme: "http",
				}},
				CreatedAt: now, UpdatedAt: now,
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed tunnels: %v", err)
	}
	for _, hit := range []domain.FrpProbeHit{
		{ID: "hit_1", TunnelID: "tun_1", RunID: "run_1", MappingName: "probe", Method: "GET", Path: "/one", RemoteAddr: "127.0.0.1:1", ReceivedAt: now},
		{ID: "hit_2", TunnelID: "tun_2", RunID: "run_2", MappingName: "probe", Method: "GET", Path: "/two", RemoteAddr: "127.0.0.1:2", ReceivedAt: now},
	} {
		if _, err := api.store.RecordFrpProbeHit(ctx, hit, sqlite.DefaultFrpProbeMaxHits); err != nil {
			t.Fatalf("record hit: %v", err)
		}
	}

	statusResponse := api.request(t, http.MethodGet, "/servers/tunnels/probe/status", nil)
	if statusResponse.StatusCode != http.StatusOK {
		t.Fatalf("probe status route = %d", statusResponse.StatusCode)
	}
	status := decodeTestJSON[map[string]any](t, statusResponse)
	if status["listenHost"] != "127.0.0.1" || status["listenPort"].(float64) != 17990 {
		t.Fatal("unexpected probe status")
	}
	if _, exists := status["hitCount"]; exists {
		t.Fatal("global listener health must not expose mixed hit counts")
	}

	hitsResponse := api.request(t, http.MethodGet, "/servers/tunnels/tun_1/probe-hits?pageSize=100", nil)
	if hitsResponse.StatusCode != http.StatusOK {
		t.Fatalf("probe hits route = %d", hitsResponse.StatusCode)
	}
	hits := decodeTestJSON[map[string]any](t, hitsResponse)
	items := hits["items"].([]any)
	if len(items) != 1 {
		t.Fatal("expected one hit item")
	}
	first := items[0].(map[string]any)
	if first["path"] != "/one" || first["tunnelId"] != "tun_1" || first["runId"] != "run_1" {
		t.Fatal("unexpected hit item")
	}
	encoded, err := json.Marshal(hits)
	if err != nil {
		t.Fatalf("marshal hits: %v", err)
	}
	if strings.Contains(string(encoded), "token") || strings.Contains(string(encoded), "frpc") || strings.Contains(string(encoded), "frps") {
		t.Fatal("probe hits contained a restricted field")
	}

	clearResponse := api.request(t, http.MethodDelete, "/servers/tunnels/tun_1/probe-hits", nil)
	if clearResponse.StatusCode != http.StatusOK {
		t.Fatalf("probe clear route = %d", clearResponse.StatusCode)
	}
	afterClear := api.request(t, http.MethodGet, "/servers/tunnels/tun_1/probe-hits", nil)
	afterClearHits := decodeTestJSON[map[string]any](t, afterClear)
	if len(afterClearHits["items"].([]any)) != 0 {
		t.Fatal("expected hits to be cleared")
	}
	otherResponse := api.request(t, http.MethodGet, "/servers/tunnels/tun_2/probe-hits", nil)
	other := decodeTestJSON[map[string]any](t, otherResponse)
	if len(other["items"].([]any)) != 1 || other["hitCount"] != float64(1) {
		t.Fatal("clearing one tunnel affected another")
	}
	for _, legacyPath := range []string{"/servers/tunnels/probe/hits", "/servers/tunnels/probe/clear"} {
		response := api.request(t, http.MethodGet, legacyPath, nil)
		if response.StatusCode == http.StatusOK {
			t.Fatalf("legacy global probe route must be removed: %s", legacyPath)
		}
		_ = response.Body.Close()
	}
}

func TestServerRuntimeEmptyActionBodiesAreStrictEmptyObject(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Servers = []domain.Object{{
			"id":          "srv_gateway",
			"name":        "Gateway",
			"publicHost":  "203.0.113.20",
			"environment": "staging",
		}}
		return nil
	}); err != nil {
		t.Fatalf("seed server: %v", err)
	}

	actions := []struct {
		path   string
		action string
	}{
		{path: "/servers/srv_gateway/docker/install", action: "install_docker_engine"},
		{path: "/servers/srv_gateway/traefik/install", action: "install_traefik_gateway"},
		{path: "/servers/srv_gateway/frps/install", action: "install_frps"},
		{path: "/servers/srv_gateway/jenkins/install", action: "install_jenkins"},
		{path: "/servers/srv_gateway/docker/uninstall", action: "uninstall_docker_engine"},
		{path: "/servers/srv_gateway/swarm/uninstall", action: "uninstall_docker_swarm"},
		{path: "/servers/srv_gateway/traefik/uninstall", action: "uninstall_traefik_gateway"},
		{path: "/servers/srv_gateway/frps/uninstall", action: "uninstall_frps"},
		{path: "/servers/srv_gateway/jenkins/uninstall", action: "uninstall_jenkins"},
	}

	for _, item := range actions {
		response := api.request(t, http.MethodPost, item.path, map[string]any{})
		if response.StatusCode != http.StatusOK {
			body := decodeTestJSON[map[string]any](t, response)
			// Uninstall may be blocked by impact analysis; body contract is the focus.
			if response.StatusCode == http.StatusConflict {
				if body["errorCode"] == "INVALID_JSON" {
					t.Fatalf("%s empty object unexpectedly returned INVALID_JSON", item.path)
				}
				continue
			}
			t.Fatalf("%s empty object status = %d", item.path, response.StatusCode)
		}
		task := decodeTestJSON[map[string]any](t, response)
		if task["action"] != item.action {
			t.Fatalf("%s returned an unexpected action, want %s", item.path, item.action)
		}
	}

	// Named Traefik regression: missing body and unknown fields must be rejected.
	traefikPath := "/servers/srv_gateway/traefik/install"
	missingBody, err := http.NewRequest(http.MethodPost, api.server.URL+traefikPath, http.NoBody)
	if err != nil {
		t.Fatalf("create missing body request: %v", err)
	}
	missingResponse, err := http.DefaultClient.Do(missingBody)
	if err != nil {
		t.Fatalf("missing body request: %v", err)
	}
	if missingResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing body status = %d", missingResponse.StatusCode)
	}
	missingPayload := decodeTestJSON[map[string]any](t, missingResponse)
	if missingPayload["errorCode"] != "INVALID_JSON" {
		t.Fatal("missing body returned the wrong error")
	}

	unknownResponse := api.request(t, http.MethodPost, traefikPath, map[string]any{"clearLocalData": true})
	if unknownResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", unknownResponse.StatusCode)
	}
	unknownPayload := decodeTestJSON[map[string]any](t, unknownResponse)
	if unknownPayload["errorCode"] != "INVALID_JSON" {
		t.Fatal("unknown field returned the wrong error")
	}

	trailing, err := http.NewRequest(http.MethodPost, api.server.URL+traefikPath, bytes.NewBufferString(`{}{}`))
	if err != nil {
		t.Fatalf("create trailing JSON request: %v", err)
	}
	trailing.Header.Set("content-type", "application/json")
	trailingResponse, err := http.DefaultClient.Do(trailing)
	if err != nil {
		t.Fatalf("trailing JSON request: %v", err)
	}
	if trailingResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("trailing JSON status = %d", trailingResponse.StatusCode)
	}
	trailingPayload := decodeTestJSON[map[string]any](t, trailingResponse)
	if trailingPayload["errorCode"] != "INVALID_JSON" {
		t.Fatal("trailing JSON returned the wrong error")
	}
}

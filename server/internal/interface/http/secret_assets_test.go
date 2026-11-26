package httpapi

import (
	"net/http"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func createTestSecretAsset(t *testing.T, api *testAPI, payload map[string]any) map[string]any {
	t.Helper()
	response := api.request(t, http.MethodPost, "/secrets/assets", payload)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create secret asset status = %d", response.StatusCode)
	}
	body := decodeTestJSON[map[string]any](t, response)
	asset, ok := body["asset"].(map[string]any)
	if !ok || asset["id"] == "" {
		t.Fatal("create response missing asset identifier")
	}
	if body["operation"] == nil {
		t.Fatal("create response missing operation")
	}
	operationID := body["operation"].(map[string]any)["id"].(string)
	confirmed := api.request(t, http.MethodPost, "/operations/runs/"+operationID+"/confirm", nil)
	if confirmed.StatusCode != http.StatusOK {
		t.Fatalf("凭据确认失败：%d", confirmed.StatusCode)
	}
	_ = confirmed.Body.Close()
	assertSecretAssetWorkerSuccess(t, api, operationID)
	return asset
}

func TestSecretAssetRoutesAreStrictAndUnified(t *testing.T) {
	api := newTestAPI(t)

	for _, path := range []string{
		"/servers/credentials",
		"/projects/source-credentials",
	} {
		response := api.request(t, http.MethodGet, path, nil)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s should be removed, got %d", path, response.StatusCode)
		}
		_ = response.Body.Close()
	}

	invalid := api.request(t, http.MethodPost, "/secrets/assets", map[string]any{
		"name":        "Bad",
		"assetType":   "ssh_key",
		"secretValue": "not-a-key",
		"unknown":     true,
	})
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", invalid.StatusCode)
	}
	_ = invalid.Body.Close()

	asset := createTestSecretAsset(t, api, map[string]any{
		"name":        "Deploy SSH",
		"assetType":   "ssh_key",
		"secretValue": validHTTPTestSSHPrivateKey,
		"description": "示例部署",
	})
	assetID := asset["id"].(string)

	updated := api.request(t, http.MethodPut, "/secrets/assets/"+assetID, map[string]any{
		"name":        "Deploy SSH 2",
		"description": "示例凭据更新",
	})
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d", updated.StatusCode)
	}
	updatedAsset := decodeTestJSON[map[string]any](t, updated)
	if updatedAsset["name"] != "Deploy SSH 2" || updatedAsset["description"] != "示例凭据更新" {
		t.Fatal("credential metadata was not updated")
	}

	rejected := api.request(t, http.MethodPut, "/secrets/assets/"+assetID, map[string]any{"secretValue": "x"})
	if rejected.StatusCode != http.StatusBadRequest {
		t.Fatalf("secret value update must be rejected, got %d", rejected.StatusCode)
	}
	_ = rejected.Body.Close()

	empty := api.request(t, http.MethodPut, "/secrets/assets/"+assetID+"/secret", map[string]any{"secretValue": ""})
	if empty.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty rotation status = %d", empty.StatusCode)
	}
	_ = empty.Body.Close()

	rotated := api.request(t, http.MethodPut, "/secrets/assets/"+assetID+"/secret", map[string]any{"secretValue": validHTTPTestSSHPrivateKey})
	if rotated.StatusCode != http.StatusOK {
		t.Fatalf("rotate status = %d", rotated.StatusCode)
	}
	rotatedBody := decodeTestJSON[map[string]any](t, rotated)
	if rotatedBody["asset"] == nil || rotatedBody["operation"] == nil {
		t.Fatal("rotate response missing asset or operation")
	}

	deleted := api.request(t, http.MethodDelete, "/secrets/assets/"+assetID, nil)
	if deleted.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", deleted.StatusCode)
	}
	_ = deleted.Body.Close()

	list := api.request(t, http.MethodGet, "/secrets/assets", nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", list.StatusCode)
	}
	items := decodeTestJSON[map[string]any](t, list)["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("expected no remaining assets, got %d", len(items))
	}
}

func TestSecretAssetDeleteReportsOutputBlockers(t *testing.T) {
	api := newTestAPI(t)
	asset := createTestSecretAsset(t, api, map[string]any{
		"name":        "Env secret",
		"assetType":   "git_token",
		"secretValue": "sample-git-value",
	})
	assetID := asset["id"].(string)
	secretRef := asset["secretRef"].(string)
	if err := api.store.MutateState(t.Context(), func(state *domain.State) error {
		state.ApplicationDeploymentConfigs = append(state.ApplicationDeploymentConfigs, domain.Object{
			"applicationId": "app_demo", "outputVariables": []domain.Object{{"key": "ACCESS", "valueSource": "secret_ref", "secretRef": secretRef}},
		})
		return nil
	}); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	blocked := api.request(t, http.MethodDelete, "/secrets/assets/"+assetID, nil)
	if blocked.StatusCode != http.StatusBadRequest {
		t.Fatalf("blocked delete status = %d", blocked.StatusCode)
	}
	_ = blocked.Body.Close()
}

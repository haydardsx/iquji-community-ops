package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net/http"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

func TestSavedSSHAssetBindingAndRotationCompleteThroughWorker(t *testing.T) {
	api := newTestAPI(t)
	serverResponse := api.request(t, http.MethodPost, "/servers", map[string]any{
		"name": "网关", "publicHost": "203.0.113.10", "environment": "staging",
	})
	if serverResponse.StatusCode != http.StatusOK {
		t.Fatalf("服务器创建失败：%d", serverResponse.StatusCode)
	}
	serverID := decodeTestJSON[map[string]any](t, serverResponse)["id"].(string)
	created := api.request(t, http.MethodPost, "/secrets/assets", map[string]any{
		"name": "网关服务器密钥", "assetType": "ssh_key", "secretValue": validHTTPTestSSHPrivateKey,
	})
	if created.StatusCode != http.StatusOK {
		t.Fatalf("凭据创建失败：%d", created.StatusCode)
	}
	body := decodeTestJSON[map[string]any](t, created)
	asset := body["asset"].(map[string]any)
	assetID := asset["id"].(string)
	operationID := body["operation"].(map[string]any)["id"].(string)
	if result, err := api.runtime.RunWorkerOnce(t.Context()); err != nil || result.Processed != 0 {
		t.Fatal("确认前工作线程不应处理凭据操作")
	}
	confirm := api.request(t, http.MethodPost, "/operations/runs/"+operationID+"/confirm", nil)
	if confirm.StatusCode != http.StatusOK {
		t.Fatalf("凭据创建操作确认失败：%d", confirm.StatusCode)
	}
	_ = confirm.Body.Close()
	assertSecretAssetWorkerSuccess(t, api, operationID)
	bound := api.request(t, http.MethodPut, "/servers/"+serverID+"/secret-assets", map[string]any{"secretAssetIds": []string{assetID}})
	if bound.StatusCode != http.StatusOK {
		t.Fatalf("凭据绑定失败：%d", bound.StatusCode)
	}
	_ = bound.Body.Close()
	state, err := api.store.LoadState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range state.Servers {
		if server["id"] != serverID {
			continue
		}
		ids := objectStringSliceHTTP(server["secretAssetIds"])
		if len(ids) != 1 || ids[0] != assetID {
			t.Fatal("创建操作执行后服务器绑定丢失")
		}
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateBlock, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		t.Fatal(err)
	}
	rotatedMaterial := string(pem.EncodeToMemory(privateBlock))
	rotated := api.request(t, http.MethodPut, "/secrets/assets/"+assetID+"/secret", map[string]any{"secretValue": rotatedMaterial})
	if rotated.StatusCode != http.StatusOK {
		t.Fatalf("凭据轮换失败：%d", rotated.StatusCode)
	}
	rotation := decodeTestJSON[map[string]any](t, rotated)
	rotationID := rotation["operation"].(map[string]any)["id"].(string)
	confirmRotation := api.request(t, http.MethodPost, "/operations/runs/"+rotationID+"/confirm", nil)
	if confirmRotation.StatusCode != http.StatusOK {
		t.Fatalf("凭据轮换操作确认失败：%d", confirmRotation.StatusCode)
	}
	_ = confirmRotation.Body.Close()
	assertSecretAssetWorkerSuccess(t, api, rotationID)
	material, err := api.store.GetSecretMaterial(t.Context(), asset["secretRef"].(string))
	if err != nil || material != rotatedMaterial {
		t.Fatalf("凭据材料未保存：%v", err)
	}
}

func assertSecretAssetWorkerSuccess(t *testing.T, api *testAPI, operationID string) {
	t.Helper()
	if result, err := api.runtime.RunWorkerOnce(t.Context()); err != nil || result.Processed != 1 {
		t.Fatal("工作线程未处理已确认的凭据操作")
	}
	response := api.request(t, http.MethodGet, "/operations/runs/"+operationID, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("操作查询失败：%d", response.StatusCode)
	}
	run := decodeTestJSON[domain.OperationRun](t, response)
	if run.Status != "succeeded" || run.ErrorCode != "" {
		t.Fatal("凭据操作未成功完成")
	}
}

package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"testing"
)

func TestTaskIdempotencyHeaderReusesRunWithoutSuppressingExplicitSubmissions(t *testing.T) {
	api := newTestAPI(t)
	request := func(key, path, body string) (int, map[string]any) {
		t.Helper()
		r, err := http.NewRequest(http.MethodPost, api.server.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, decodeTestJSON[map[string]any](t, response)
	}
	path := "/servers/server_example/docker/install"
	status, first := request("request_one", path, `{}`)
	if status != http.StatusOK {
		t.Fatalf("首次提交失败: %d", status)
	}
	status, retry := request("request_one", path, `{}`)
	if status != http.StatusOK || retry["id"] != first["id"] {
		t.Fatalf("重试没有复用运行: %d", status)
	}
	status, conflict := request("request_one", "/servers/other_server/docker/install", `{}`)
	if status != http.StatusConflict || conflict["errorCode"] != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Fatalf("同键不同输入没有冲突: %d", status)
	}
	status, second := request("request_two", path, `{}`)
	if status != http.StatusOK || second["id"] == first["id"] {
		t.Fatalf("明确新提交被去重: %d", status)
	}
	state, err := api.store.LoadState(context.Background())
	if err != nil || len(state.Tasks) != 2 {
		t.Fatalf("重试创建了多余任务: %d %v", len(state.Tasks), err)
	}
}

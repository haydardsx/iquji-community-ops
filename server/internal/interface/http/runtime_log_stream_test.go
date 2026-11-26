package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 第二个 SQLite 连接模拟独立 Worker，流与列表必须返回同一条已提交记录。
func TestRuntimeLogStreamFollowsCommittedScopedRows(t *testing.T) {
	api := newTestAPI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, api.server.URL+"/logs/runtime/stream?operationRunId=run_example", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	readData := func() string {
		t.Helper()
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				return data
			}
		}
		t.Fatalf("订阅提前结束：%v", scanner.Err())
		return ""
	}
	if data := readData(); !strings.Contains(data, `"type":"snapshot"`) {
		t.Fatal("订阅缺少初始快照")
	}
	writer, err := sqlite.Open(ctx, api.runtime.Config.DatabasePath, api.runtime.Config.RepositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	task := domain.Task{ID: "run_example", TargetKind: "server", TargetID: "server_example"}
	appendEntry := func(state *domain.State, entry domain.RuntimeLog) {
		state.RuntimeLogs = append(state.RuntimeLogs, entry)
	}
	failed := domain.NewRuntimeLog(task, "worker", "discard", "info", "事务已回滚", domain.Now())
	if err := writer.MutateState(ctx, func(state *domain.State) error { appendEntry(state, failed); return errors.New("TEST_ROLLBACK") }); err == nil {
		t.Fatal("预期回滚")
	}
	expected := domain.NewRuntimeLog(task, "worker", "complete", "error", "TEST_OPERATION_FAILED", domain.Now())
	if err := writer.MutateState(ctx, func(state *domain.State) error {
		other := task
		other.ID = "other"
		appendEntry(state, domain.NewRuntimeLog(other, "worker", "complete", "info", "其他运行", domain.Now()))
		appendEntry(state, expected)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type string
		Item domain.RuntimeLog
	}
	if err := json.Unmarshal([]byte(readData()), &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "log" || event.Item != expected {
		t.Fatal("订阅事件不符")
	}
	page := decodeTestJSON[struct{ Items []domain.RuntimeLog }](t, api.request(t, http.MethodGet, "/logs/runtime?operationRunId=run_example", nil))
	if len(page.Items) != 1 || page.Items[0] != event.Item {
		t.Fatal("查询与订阅不一致")
	}
}

package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type applicationLogTestRemote struct {
	*fakeApplicationRemote
	read func(context.Context, string, int) (string, bool, error)
}

func (r *applicationLogTestRemote) RunLogs(ctx context.Context, command string, limit int) (string, bool, error) {
	return r.read(ctx, command, limit)
}

func applicationLogTestExecutor(t *testing.T, driver, owner, app string, read func(context.Context, string, int) (string, bool, error)) *SystemApplicationRuntimeExecutor {
	t.Helper()
	remote := &applicationLogTestRemote{read: read, fakeApplicationRemote: &fakeApplicationRemote{run: func(command string) (string, bool, error) {
		switch {
		case strings.HasPrefix(command, "docker info"):
			return `{"LocalNodeState":"active","ControlAvailable":true,"Cluster":{"ID":"cluster_one"}}`, false, nil
		case strings.HasPrefix(command, "docker service ls"):
			return "service_one", false, nil
		case strings.HasPrefix(command, "docker service inspect"):
			encoded, _ := json.Marshal([]domain.Object{{"ID": "service_one", "Spec": domain.Object{"Name": "web", "Labels": domain.Object{"iquops.owner": owner, "iquops.application-id": app, "iquops.service-slot": "web"}, "Mode": domain.Object{"Replicated": domain.Object{"Replicas": 0}}, "TaskTemplate": domain.Object{"LogDriver": domain.Object{"Name": driver}}}}})
			return string(encoded), false, nil
		default:
			t.Fatal("读取应用日志不应读取任务/操作或执行其他命令")
			return "", false, nil
		}
	}}}
	return &SystemApplicationRuntimeExecutor{dial: func(context.Context, ApplicationServiceInput) (applicationServiceRemote, error) { return remote, nil }}
}

func TestApplicationLogsReadActualStdoutAndStderrWithoutManualConfiguration(t *testing.T) {
	for _, driver := range []string{"json-file", "journald", ""} {
		t.Run("driver_"+driver, func(t *testing.T) {
			executor := applicationLogTestExecutor(t, driver, "iquops", "app_one", func(_ context.Context, command string, limit int) (string, bool, error) {
				if command != "docker service logs --raw --timestamps --tail 200 --since 30m 'service_one'" || limit != applicationLogMaxBytes {
					t.Fatalf("日志目标或限制不正确，实际限制 %d", limit)
				}
				return "2026-01-01T00:00:02.000000000Z stderr 应用请求失败\n2026-01-01T00:00:01.123456789Z stdout 应用已启动", false, nil
			})
			result, err := executor.Logs(context.Background(), ApplicationServiceInput{ApplicationID: "app_one", ClusterID: "cluster_one"}, "web")
			if err != nil || result.Availability != "available" || result.Truncated || len(result.Items) != 2 {
				t.Fatal("真实应用输出读取失败")
			}
			if result.Items[0].Timestamp != "2026-01-01T00:00:01.123456789Z" || result.Items[0].Message != "stdout 应用已启动" || result.Items[1].Message != "stderr 应用请求失败" {
				t.Fatal("没有按真实时间呈现应用输出")
			}
			for _, item := range result.Items {
				if item.Source != "swarm" || item.ServiceSlotKey != "web" {
					t.Fatal("应用日志丢失服务归属")
				}
			}
		})
	}
}

func TestApplicationLogsKeepOwnershipDriverAndReadErrorsDistinct(t *testing.T) {
	for _, test := range []struct {
		name, owner, app, driver, slot, output, code string
		readErr                                      error
	}{
		{name: "其他应用", owner: "iquops", app: "app_other", driver: "json-file", slot: "web", code: "APPLICATION_SERVICE_OWNERSHIP_MISMATCH"},
		{name: "非受管服务", owner: "other", app: "app_one", driver: "json-file", slot: "web", code: "APPLICATION_SERVICE_OWNERSHIP_MISMATCH"},
		{name: "错误槽位", owner: "iquops", app: "app_one", driver: "json-file", slot: "other", code: "APPLICATION_LOG_SERVICE_REQUIRED"},
		{name: "历史驱动不支持", owner: "iquops", app: "app_one", driver: "syslog", slot: "web", code: "APPLICATION_LOG_DRIVER_UNSUPPORTED"},
		{name: "默认驱动不支持", owner: "iquops", app: "app_one", slot: "web", output: "configured logging driver does not support reading", readErr: errors.New("remote failure"), code: "APPLICATION_LOG_DRIVER_UNSUPPORTED"},
		{name: "真实读取失败", owner: "iquops", app: "app_one", driver: "json-file", slot: "web", output: "credential-like diagnostic must not be returned", readErr: errors.New("remote failure"), code: "APPLICATION_LOG_READ_FAILED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := applicationLogTestExecutor(t, test.driver, test.owner, test.app, func(context.Context, string, int) (string, bool, error) { return test.output, false, test.readErr })
			result, err := executor.Logs(context.Background(), ApplicationServiceInput{ApplicationID: "app_one", ClusterID: "cluster_one"}, test.slot)
			if err == nil || err.Error() != test.code || len(result.Items) != 0 {
				t.Fatal("错误被混成空成功或泄露诊断")
			}
		})
	}
	executor := applicationLogTestExecutor(t, "json-file", "iquops", "app_one", func(context.Context, string, int) (string, bool, error) { return "", false, nil })
	result, err := executor.Logs(context.Background(), ApplicationServiceInput{ApplicationID: "app_one"}, "web")
	if err != nil || result.Availability != "available" || len(result.Items) != 0 || result.Truncated {
		t.Fatal("真实零输出不能伪装成错误")
	}
}

func TestApplicationLogLimitAppliesToWholeServiceAndKeepsLatestOutput(t *testing.T) {
	lines := []string{}
	for i := 0; i < 450; i++ {
		stamp := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC).Format(time.RFC3339Nano)
		lines = append(lines, stamp+" "+fmt.Sprintf("message-%03d", i))
	}
	items, truncated := parseApplicationServiceLogs(strings.Join(lines, "\n"), ApplicationServiceState{ServiceID: "service_one", ServiceSlotKey: "web"}, nil, false)
	if !truncated || len(items) != 200 || items[0].Message != "message-250" || items[199].Message != "message-449" {
		t.Fatal("多副本输出没有按全服务限制保留最新200行")
	}
	large := strings.Repeat("日志", applicationLogMaxBytes)
	items, truncated = parseApplicationServiceLogs("2026-01-01T00:00:00Z "+large, ApplicationServiceState{ServiceID: "service_one", ServiceSlotKey: "web"}, nil, false)
	if !truncated || len(items) != 1 || len(items[0].Message)+len(items[0].Timestamp) > applicationLogMaxBytes || !utf8.ValidString(items[0].Message) {
		t.Fatal("单行输出绕过字节限制或截断了UTF-8字符")
	}
	writer := &applicationBoundedOutput{limit: 12, keepTail: true}
	for _, part := range []string{"old-output", "-newest", "-tail"} {
		if _, err := writer.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if writer.buffer.Len() != 12 || writer.buffer.String() != "-newest-tail" || !writer.truncated {
		t.Fatalf("有界读取保留了旧输出: %q", writer.buffer.String())
	}
}

func TestApplicationLogReadStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	executor := applicationLogTestExecutor(t, "json-file", "iquops", "app_one", func(ctx context.Context, _ string, _ int) (string, bool, error) {
		<-ctx.Done()
		return "", false, ctx.Err()
	})
	if _, err := executor.Logs(ctx, ApplicationServiceInput{ApplicationID: "app_one"}, "web"); err == nil || err.Error() != "APPLICATION_LOG_READ_FAILED" {
		t.Fatalf("取消读取被当作空成功: %v", err)
	}
}

func TestApplicationLogResponseAlsoBoundsJSONEscapingAndMetadata(t *testing.T) {
	for _, test := range []struct{ name, content string }{{"单字节精确边界", strings.Repeat("a", applicationLogMaxBytes*2)}, {"JSON转义", strings.Repeat("\"\t", applicationLogMaxBytes)}} {
		t.Run(test.name, func(t *testing.T) {
			executor := applicationLogTestExecutor(t, "json-file", "iquops", "app_one", func(context.Context, string, int) (string, bool, error) {
				return "2026-01-01T00:00:00Z " + test.content, true, nil
			})
			result, err := executor.Logs(context.Background(), ApplicationServiceInput{ApplicationID: "app_one"}, "web")
			var encoded bytes.Buffer
			encodeErr := json.NewEncoder(&encoded).Encode(result)
			if err != nil || encodeErr != nil || !result.Truncated || len(result.Items) != 1 || encoded.Len() > applicationLogMaxBytes {
				t.Fatalf("HTTP JSON转义或尾换行绕过响应字节上限: length=%d err=%v", encoded.Len(), err)
			}
		})
	}
}

func TestApplicationLogServiceRedactsCurrentAndHistoricalSecretsAndExcludesOperations(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	for ref, value := range map[string]string{"secret_current": "example-current-value", "secret_previous": "example-previous-value"} {
		if err := store.UpsertSecretMaterial(ctx, ref, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers[0]["health"] = domain.Object{"metrics": domain.Object{"jenkins": false, "jenkinsStatus": "unavailable", "jenkinsBundleVersion": "v8-jenkins-2.568.1"}}
		state.Applications = []domain.Object{{"id": "app_one"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app_one", "deploymentManagerServerId": "srv_gateway", "runtimeLogConfig": domain.Object{"enabled": false}, "environmentVariables": []domain.Object{{"key": "VALUE", "valueSource": "secret_ref", "scope": "global", "secretRef": "secret_current"}}}}
		state.ReleasePlans = []domain.Object{{"applicationId": "app_one", "configSnapshot": []domain.Object{{"key": "VALUE", "valueSource": "secret_ref", "scope": "service", "serviceSlotKey": "web", "secretRef": "secret_previous"}}}}
		state.RuntimeLogs = []domain.RuntimeLog{{ID: "operation_only", TargetKind: "application", TargetID: "app_one", Message: "操作阶段不应成为应用运行输出"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime.ApplicationRuntimeExecutor = applicationLogTestExecutor(t, "json-file", "iquops", "app_one", func(context.Context, string, int) (string, bool, error) {
		return "2026-01-01T00:00:00Z received example-current-value and example-previous-value\n2026-01-01T00:00:01Z token=example-token Authorization: Bearer example-bearer", false, nil
	})
	result, err := runtime.ApplicationServiceLogs(ctx, "app_one", "web")
	if err != nil || result.Availability != "available" || len(result.Items) != 2 {
		t.Fatal("无需手工配置的真实日志读取失败")
	}
	encoded, _ := json.Marshal(result)
	for _, value := range []string{"example-current-value", "example-previous-value", "example-token", "example-bearer", "操作阶段不应成为应用运行输出"} {
		if strings.Contains(string(encoded), value) {
			t.Fatal("应用日志泄露秘密或混入操作阶段")
		}
	}
	if !strings.Contains(string(encoded), "[REDACTED]") {
		t.Fatal("已知秘密值没有被遮蔽")
	}
}

func TestApplicationLogsFailClosedWhenCurrentOrHistoricalRedactionMaterialIsMissing(t *testing.T) {
	for _, historical := range []bool{false, true} {
		name := "当前引用"
		if historical {
			name = "历史发布引用"
		}
		t.Run(name, func(t *testing.T) {
			runtime, store, ctx := newFrpRuntime(t)
			seedFrpServer(t, ctx, store, true)
			if err := store.MutateState(ctx, func(state *domain.State) error {
				state.Applications = []domain.Object{{"id": "app_one"}}
				config := domain.Object{"applicationId": "app_one", "deploymentManagerServerId": "srv_gateway"}
				variable := domain.Object{"key": "VALUE", "valueSource": "secret_ref", "scope": "global", "secretRef": "secret_missing"}
				if historical {
					state.ReleasePlans = []domain.Object{{"applicationId": "app_one", "configSnapshot": []domain.Object{variable}}}
				} else {
					config["environmentVariables"] = []domain.Object{variable}
				}
				state.ApplicationDeploymentConfigs = []domain.Object{config}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			runtime.ApplicationRuntimeExecutor = applicationLogTestExecutor(t, "json-file", "iquops", "app_one", func(context.Context, string, int) (string, bool, error) {
				t.Fatal("脱敏材料缺失时仍读取了应用输出")
				return "", false, nil
			})
			logs, err := runtime.ApplicationServiceLogs(ctx, "app_one", "web")
			if err != nil || logs.Availability != "unavailable" || logs.UnavailableReason != "APPLICATION_LOG_REDACTION_UNAVAILABLE" || len(logs.Items) != 0 {
				t.Fatal("缺失脱敏材料没有阻断日志")
			}
			runtime.ApplicationRuntimeExecutor = &fakeApplicationRuntimeExecutor{snapshot: ApplicationRuntimeSnapshot{Availability: "available", Status: "stopped", CheckedAt: domain.Now(), Services: []ApplicationServiceState{}}}
			snapshot, err := runtime.ApplicationRuntime(ctx, "app_one")
			if err != nil || snapshot.Availability != "available" {
				t.Fatal("日志脱敏前置错误阻断了运行态查询")
			}
		})
	}
}

func TestApplicationLogsRedactPrivateKeyFragmentsBeforeReturningTruncatedOutput(t *testing.T) {
	boundary := func(label string) string { return fmt.Sprintf("-----%s PRIVATE KEY-----", label) }
	key := strings.Join([]string{boundary("BEGIN"), "example-known-private-key-line", boundary("END")}, "\n")
	for _, test := range []struct {
		name, output string
		secrets      []string
		truncated    bool
	}{
		{name: "截断首行", output: "example-partial-private-fragment\n2026-01-01T00:00:00Z ordinary output", truncated: true},
		{name: "只有尾部边界", output: "2026-01-01T00:00:00Z example-partial-private-fragment\n2026-01-01T00:00:01Z " + boundary("END") + "\n2026-01-01T00:00:02Z ordinary output", truncated: true},
		{name: "只有起始边界", output: "2026-01-01T00:00:00Z " + boundary("BEGIN") + "\n2026-01-01T00:00:01Z example-partial-private-fragment"},
		{name: "已知PEM被Docker拆行", output: "2026-01-01T00:00:00Z example-known-private-key-line", secrets: []string{key}},
	} {
		t.Run(test.name, func(t *testing.T) {
			items, truncated := parseApplicationServiceLogs(test.output, ApplicationServiceState{ServiceID: "service_one", ServiceSlotKey: "web"}, test.secrets, test.truncated)
			encoded, _ := json.Marshal(items)
			if strings.Contains(string(encoded), "example-partial-private-fragment") || strings.Contains(string(encoded), "example-known-private-key-line") || strings.Contains(string(encoded), "PRIVATE KEY-----") {
				t.Fatal("私钥边界或已知材料的尾片被返回")
			}
			if test.truncated && !truncated {
				t.Fatal("丢弃不完整片段后未保留截断提示")
			}
		})
	}
}

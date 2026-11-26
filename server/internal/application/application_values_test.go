package application

import (
	"errors"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestOutputReferencesTrackExactCredentialRevisionAndVisibility(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	asset := seedSecretAsset(t, ctx, r, domain.Object{"assetType": "application_secret", "name": "应用令牌", "secretValue": "example-value-one"})
	owner := domain.Object{"id": "app_producer", "projectId": "p1"}
	consumer := domain.Object{"id": "app_consumer", "projectId": "p2"}
	output := domain.Object{"id": "export_access", "key": "ACCESS", "visibility": "cross_project", "valueSource": "secret_ref", "secretRef": asset["secretRef"]}
	producerConfig := domain.Object{"applicationId": owner["id"], "outputVariables": []domain.Object{output}}
	v := domain.Object{"key": "TOKEN", "scope": "global", "stage": "build", "valueSource": "output_ref", "outputId": "export_access", "outputApplicationId": owner["id"]}
	cfg := domain.Object{"applicationId": consumer["id"], "environmentVariables": []domain.Object{v}}
	state := readState(t, ctx, r)
	state.Applications = []domain.Object{owner, consumer}
	state.ApplicationDeploymentConfigs = []domain.Object{producerConfig, cfg}
	snapshot, err := applicationInputSnapshot(state, "app_consumer", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot[0]["plainValue"] != nil || snapshot[0]["secretRef"] != asset["secretRef"] || snapshot[0]["materialRevisionRef"] == nil {
		t.Fatal("快照应只存受控材料修订")
	}
	recordAppliedApplicationInputs(&state, "app_consumer", snapshot, "build", "runtime")
	options := availableApplicationOutputs(state, "p2")
	if len(options) != 1 || options[0]["sensitive"] != true || options[0]["secretRef"] != nil {
		t.Fatal("输出目录必须只返回元数据")
	}
	output["visibility"] = "project"
	if _, err := applicationInputSnapshot(state, "app_consumer", cfg); err == nil {
		t.Fatal("禁止跨项目读取未共享的输出")
	}
	if err := validateApplicationOutputs(state, owner, producerConfig); err == nil {
		t.Fatal("有外部引用时不能撤回共享")
	}
	output["visibility"] = "cross_project"
	if err := store.MutateState(ctx, func(s *domain.State) error {
		s.Applications = state.Applications
		s.ApplicationDeploymentConfigs = state.ApplicationDeploymentConfigs
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = r.RotateSecretAsset(ctx, stringValue(asset["id"]), domain.Object{"secretValue": "example-value-two"})
	if err != nil {
		t.Fatal(err)
	}
	state = readState(t, ctx, r)
	consumer = findObject(state.Applications, "app_consumer")
	if applicationInputStatus(state, consumer) != "rebuild" {
		t.Fatal("轮换应点亮构建输入")
	}
	view := applicationWithInputStatus(state, consumer)
	changes := objectSlice(view["inputChanges"])
	if len(changes) != 1 || changes[0]["key"] != "TOKEN" || changes[0]["outputName"] != "ACCESS" || changes[0]["credentialName"] != "应用令牌" || changes[0]["secretRef"] != nil || changes[0]["plainValue"] != nil || view["appliedInputRevisions"] != nil {
		t.Fatal("构建提醒应显示变动的变量、上游输出及凭据名称")
	}
	values, err := r.resolvedStageValues(ctx, snapshot, "build", "")
	if err != nil || values["TOKEN"] != "example-value-one" {
		t.Fatal("运行中的发布使用原快照")
	}
	recordAppliedApplicationInputs(&state, "app_consumer", snapshot, "build", "runtime")
	if applicationInputStatus(state, consumer) != "rebuild" {
		t.Fatal("旧任务不能清除新改动")
	}
	latest, err := applicationInputSnapshot(state, "app_consumer", cfg)
	if err != nil {
		t.Fatal(err)
	}
	recordAppliedApplicationInputs(&state, "app_consumer", latest, "build", "runtime")
	if applicationInputStatus(state, consumer) != "" {
		t.Fatal("成功应用新版本后应清除提醒")
	}
}

func TestInputsRejectCredentialConfusionAndOutputCycles(t *testing.T) {
	s := domain.State{Applications: []domain.Object{{"id": "a", "projectId": "p"}, {"id": "b", "projectId": "p"}}, SecretAssets: []domain.Object{{"id": "ssh", "assetType": "ssh_key", "secretRef": "deployment_key"}}}
	if _, err := normalizeApplicationVariables(s, []domain.Object{{"key": "VALUE", "stage": "runtime", "valueSource": "secret_ref", "secretRef": "deployment_key"}}, nil, "a", false); err == nil {
		t.Fatal("部署私钥不可作为应用输入")
	}
	cfg := func(owner, id, source, ref string) domain.Object {
		return domain.Object{"applicationId": owner, "environmentVariables": []domain.Object{{"key": "IN", "valueSource": "output_ref", "outputId": ref}}, "outputVariables": []domain.Object{{"id": id, "key": "OUT", "visibility": "project", "valueSource": source, "variableKey": "IN"}}}
	}
	a, b := cfg("a", "out_a", "variable", "out_b"), cfg("b", "out_b", "variable", "out_a")
	s.ApplicationDeploymentConfigs = []domain.Object{a, b}
	if err := validateApplicationOutputs(s, s.Applications[0], a); err == nil || err.Error() != "APPLICATION_OUTPUT_CYCLE" {
		t.Fatal("应在保存阶段拒绝循环依赖", err)
	}
}

func TestTemplateReleasedInputsUseOnlyObservedAddresses(t *testing.T) {
	app := domain.Object{"id": "pub", "projectId": "p", "runtimeSnapshot": domain.Object{"availability": "available", "services": []domain.Object{{"serviceSlotKey": "main", "serviceName": "db-service", "serverId": "node", "runningReplicas": 1, "ports": []domain.Object{{"name": "db", "containerPort": 5432, "publishedPort": 15432, "internalVerified": true, "publishedVerified": true, "publishedHost": "192.0.2.2"}}}}}}
	out := domain.Object{"id": "out", "key": "PORT", "visibility": "project", "valueSource": "service_port", "serviceSlotKey": "main", "portName": "db"}
	source := domain.Object{"applicationId": "pub", "deploymentManagerServerId": "gateway", "outputVariables": []domain.Object{out}}
	cfg := domain.Object{"applicationId": "consumer", "deploymentManagerServerId": "gateway", "environmentVariables": []domain.Object{{"key": "PORT", "valueSource": "output_ref", "outputId": "out"}}}
	s := domain.State{Applications: []domain.Object{app, {"id": "consumer", "projectId": "p"}}, Servers: []domain.Object{{"id": "gateway", "health": domain.Object{"metrics": domain.Object{"swarmClusterId": "demo"}}}, {"id": "node", "publicHost": "192.0.2.2"}}, ApplicationDeploymentConfigs: []domain.Object{source, cfg}}
	for _, tt := range []struct{ source, mode, want string }{{"service_port", "internal", "5432"}, {"service_port", "published", "15432"}, {"service_host", "internal", "db-service"}, {"service_host", "published", "192.0.2.2"}} {
		out["valueSource"], out["endpointMode"] = tt.source, tt.mode
		values, err := applicationInputSnapshot(s, "consumer", cfg)
		if err != nil || values[0]["plainValue"] != tt.want {
			t.Fatal(tt, values, err)
		}
	}
}

func TestOutputSourceProtectionListsConsumersAndRejectsMissingPorts(t *testing.T) {
	s := domain.State{Applications: []domain.Object{{"id": "api", "name": "后端", "projectId": "p"}, {"id": "web", "name": "前端", "projectId": "p"}, {"id": "other", "name": "其他消费者", "projectId": "q"}}}
	swarm := domain.Object{"services": []domain.Object{{"slotKey": "main"}}, "ports": []domain.Object{{"name": "http", "targetPort": 3000, "protocol": "http"}}}
	cfg := domain.Object{"applicationId": "api", "swarmRuntime": swarm, "outputVariables": []domain.Object{
		{"id": "out", "key": "PORT", "visibility": "project", "valueSource": "service_port", "serviceSlotKey": "main", "portName": "db"},
		{"id": "url", "key": "URL", "visibility": "project", "valueSource": "route_url", "routeId": "route"},
	}}
	consumer := domain.Object{"applicationId": "web", "environmentVariables": []domain.Object{{"key": "PORT", "stage": "runtime", "outputApplicationId": "api", "valueSource": "output_ref", "outputId": "out"}, {"key": "URL", "valueSource": "output_ref", "outputApplicationId": "api", "outputId": "url"}},
		"outputVariables": []domain.Object{{"id": "forward", "key": "FORWARD", "visibility": "cross_project", "valueSource": "variable", "variableKey": "PORT"}}}
	other := domain.Object{"applicationId": "other", "environmentVariables": []domain.Object{{"key": "FORWARD", "stage": "runtime", "valueSource": "output_ref", "outputId": "forward", "outputApplicationId": "web"}}}
	s.ApplicationDeploymentConfigs = []domain.Object{cfg, consumer, other}
	s.Routes = []domain.Object{{"id": "route", "applicationId": "api"}}
	check := func(err error, code string, n int) {
		t.Helper()
		var blocked *ApplicationOutputUsageError
		if !errors.As(err, &blocked) || blocked.Code != code || len(blocked.Applications) != n {
			t.Fatal("阻断原因或受影响应用错误", err)
		}
		for _, a := range blocked.Applications {
			if a.Name == "" || a.ID == "" {
				t.Fatal("缺少阻断应用名称或 ID")
			}
		}
	}
	check(validateApplicationOutputs(s, s.Applications[0], cfg), "APPLICATION_OUTPUT_PORT_REQUIRED", 3)
	check(validateRouteOutputDeletion(s, "api", "route"), "APPLICATION_ROUTE_OUTPUT_IN_USE", 2)
	check(validateConfigurationDeletion(s, "applications", "api"), "APPLICATION_OUTPUT_IN_USE", 2)
	objectSlice(cfg["outputVariables"])[0]["portName"] = "http"
	if err := validateApplicationOutputs(s, s.Applications[0], cfg); err != nil {
		t.Fatal(err)
	}
}

func TestVariableWriteRequiresStageOwnerAndSupportsPackageServiceScope(t *testing.T) {
	state := domain.State{Applications: []domain.Object{{"id": "app", "projectId": "p"}, {"id": "producer", "projectId": "p"}},
		ApplicationDeploymentConfigs: []domain.Object{{"applicationId": "producer", "outputVariables": []domain.Object{{"id": "out", "visibility": "project"}}}}}
	slots := []domain.Object{{"slotKey": "worker"}}
	v := domain.Object{"key": "VALUE", "valueSource": "output_ref", "outputId": "out", "scope": "service", "serviceSlotKey": "worker"}
	if _, err := normalizeApplicationVariables(state, []domain.Object{v}, slots, "app", false); err == nil || err.Error() != "APPLICATION_VARIABLE_STAGE_INVALID" {
		t.Fatal("新增变量必须显式声明阶段", err)
	}
	v["stage"] = "build"
	if _, err := normalizeApplicationVariables(state, []domain.Object{v}, slots, "app", false); err == nil || err.Error() != "APPLICATION_OUTPUT_REFERENCE_INVALID" {
		t.Fatal("输出引用必须包含生产者应用 ID", err)
	}
	v["outputApplicationId"] = "producer"
	rows, err := normalizeApplicationVariables(state, []domain.Object{v}, slots, "app", false)
	if err != nil || rows[0]["serviceSlotKey"] != "worker" {
		t.Fatal("丢失构建输入的槽位", err)
	}
	config := domain.Object{"applicationId": "app", "sourceConfig": domain.Object{"provider": "npm"}, "environmentVariables": rows}
	if err = validateApplicationOutputs(state, state.Applications[0], config); err != nil {
		t.Fatal(err)
	}
	config["sourceConfig"] = domain.Object{"provider": "git"}
	if err = validateApplicationOutputs(state, state.Applications[0], config); err != nil {
		t.Fatal("Git 分槽构建应接受槽位覆盖", err)
	}
}

func TestJenkinsServiceBuildInputsOverrideSensitivity(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(s *domain.State) error {
		s.SecretAssets = append(s.SecretAssets, domain.Object{"id": "demo", "assetType": "application_secret", "purpose": "application_variables", "secretRef": "test_credential"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSecretMaterial(ctx, "test_credential", "example-material"); err != nil {
		t.Fatal(err)
	}
	inputs := []domain.Object{
		{"stage": "build", "scope": "service", "serviceSlotKey": "frontend", "key": "PUBLIC", "valueSource": "secret_ref", "secretRef": "test_credential"},
		{"stage": "build", "scope": "service", "serviceSlotKey": "frontend", "key": "PRIVATE", "valueSource": "plain_text", "plainValue": "override"},
		{"stage": "build", "scope": "global", "key": "PUBLIC", "valueSource": "plain_text", "plainValue": "global"},
		{"stage": "build", "scope": "global", "key": "PRIVATE", "valueSource": "secret_ref", "secretRef": "test_credential"},
	}
	for _, slot := range []string{"", "other", "frontend"} {
		public, secret, err := r.jenkinsBuildInputs(ctx, inputs, slot)
		if err != nil || len(secret) != 1 || len(public) != 1 {
			t.Fatal("Git 构建材料拆分错误", err)
		}
		if slot == "frontend" && (public["PRIVATE"] != "override" || secret["PUBLIC"] != "example-material") || slot != "frontend" && (public["PUBLIC"] != "global" || secret["PRIVATE"] != "example-material") {
			t.Fatal("服务槽位影响了其他构建组或秘密进入公开材料")
		}
	}
}

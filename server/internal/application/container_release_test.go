package application

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestApplicationTemplatesCreateIndependentDrafts(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	catalog, err := r.ApplicationTemplates(ctx)
	if err != nil || len(objectSlice(catalog["items"])) != len(domain.DefaultApplicationTemplates()) {
		t.Fatal("应用模板目录不可用或模板数不符")
	}
	for _, tmpl := range objectSlice(catalog["items"]) {
		id := stringValue(tmpl["id"])
		app, err := r.CreateObject(ctx, "applications", "app", domain.Object{"projectId": domain.BuiltinProjectID, "name": tmpl["name"], "templateId": id})
		if err != nil {
			t.Fatal(id, err)
		}
		state, err := store.LoadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cfg := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", stringValue(app["id"]))
		swarm := objectMapFromAny(cfg["swarmRuntime"])
		if _, err := validateApplicationSourceConfig(objectMapFromAny(cfg["sourceConfig"])); err != nil {
			t.Fatal(id, err)
		}
		if err := validateContainerRuntime(swarm); err != nil {
			t.Fatal(id, err)
		}
		if id == "demo-frontend" || id == "demo-backend" {
			wantPort, wantSlots := 80, 1
			if id == "demo-backend" {
				wantPort, wantSlots = 3000, 2
			}
			source := objectMapFromAny(cfg["sourceConfig"])
			if source["provider"] != "git" || source["ref"] != "main" || source["jenkinsfilePath"] != "Jenkinsfile" || numberValue(swarm["containerPort"]) != wantPort || len(objectSlice(swarm["services"])) != wantSlots || len(objectSlice(cfg["environmentVariables"])) < 3 || len(state.Tasks) != 0 {
				t.Fatal(id, "演示模板缺少 Git 配置、运行槽位或变量")
			}
			if err := validateApplicationTemplate(tmpl); err != nil {
				t.Fatal(id, "模板无法导入", err)
			}
			continue
		}
		if !isContainerSource(objectMapFromAny(cfg["sourceConfig"])) || len(objectSlice(swarm["volumes"])) != 1 || len(objectSlice(swarm["services"])) != 1 || len(objectSlice(cfg["outputVariables"])) < 2 {
			t.Fatal(id, "模板未产生完整应用草稿")
		}
		if len(state.Tasks) != 0 {
			t.Fatal("实例化模板不应调度远端工作")
		}
		if id == "gitea" {
			name, e := routeHTTPPort(swarm, "")
			if e != nil || name == "" {
				t.Fatal(id, e)
			}
			if _, e = routeHTTPPort(swarm, "ssh"); e == nil {
				t.Fatal("原生 TCP 端口不能发布 HTTP 路由")
			}
		}
	}
	if err := r.DeleteObject(ctx, "projects", domain.BuiltinProjectID); err == nil {
		t.Fatal("内置项目可删除")
	}
}

func TestPMailTemplateOnlyPublishesMailProtocols(t *testing.T) {
	template := findObject(domain.DefaultApplicationTemplates(), "pmail")
	swarm := objectMapFromAny(objectMapFromAny(template["config"])["swarmRuntime"])
	labels := domain.Object{}
	ports := buildServiceEndpoints(swarm, domain.Object{}, labels)
	if labels["iquops.port.web"] != "http/80" || len(ports) != 5 {
		t.Fatal("Web 应通过网关访问，仅发布邮件端口")
	}
	for _, number := range []int{25, 465, 587, 993, 995} {
		found := false
		for _, port := range ports {
			if port["TargetPort"] == number && port["PublishedPort"] == number && port["Protocol"] == "tcp" {
				found = true
			}
		}
		if !found {
			t.Errorf("缺少 TCP 邮件端口 %d", number)
		}
	}
}

func TestTemplateDraftValidatesStructureBeforeBindingCredentials(t *testing.T) {
	state := domain.EmptyState()
	app := domain.Object{"id": "app_example", "projectId": domain.BuiltinProjectID}
	config := domain.Object{
		"sourceConfig":         domain.Object{"provider": "image", "image": "example/service:1"},
		"swarmRuntime":         domain.Object{"overlayNetwork": "example"},
		"environmentVariables": []domain.Object{{"key": "TOKEN", "valueSource": "secret_ref", "stage": "runtime", "scope": "global", "isRequired": true}},
	}
	bad := cloneObject(config)
	bad["outputVariables"] = []domain.Object{{"key": "BAD", "valueSource": "service_port", "serviceSlotKey": "missing", "visibility": "project"}}
	if err := createTemplateDraft(&state, app, bad); err == nil || len(state.ApplicationDeploymentConfigs) != 0 {
		t.Fatal("无效输出被保存为应用草稿")
	}
	for _, field := range []string{"swarmRuntime", "sourceConfig"} {
		bad = cloneObject(config)
		delete(bad, field)
		if err := createTemplateDraft(&state, app, bad); err == nil {
			t.Fatal("模板缺少必要配置", field)
		}
	}
	if err := createTemplateDraft(&state, app, config); err != nil {
		t.Fatal(err)
	}
	if err := normalizeApplicationConfiguration(state, app, config, false); err == nil || err.Error() != "APPLICATION_VARIABLE_SECRET_REQUIRED" {
		t.Fatal("保存模板实例必须绑定应用凭据", err)
	}
}

func TestPackageRecipesPinVersionsAndKeepSecretsOutOfBuildContext(t *testing.T) {
	for _, provider := range []string{"apt", "dnf", "yum", "npm", "pypi"} {
		cfg := domain.Object{"provider": provider, "baseImage": "example:1", "packageName": "sample", "version": "1.2.3", "command": []string{"sample", "--serve"}}
		if _, err := validateContainerSource(cfg); err != nil {
			t.Fatal(provider, err)
		}
		raw, flags, err := buildContainerPackage("example@sha256:"+strings.Repeat("a", 64), cfg, map[string]string{"TOKEN": "fixture-only"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(flags, "--secret") {
			t.Fatal("构建凭据未挂载")
		}
		reader := tar.NewReader(bytes.NewReader(raw))
		count := 0
		for {
			h, e := reader.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(reader)
			if e != nil {
				t.Fatal(e)
			}
			if strings.HasPrefix(h.Name, "context/") {
				count++
				text := string(b)
				if strings.Contains(text, "fixture-only") || !strings.Contains(text, "/run/secrets/") || !strings.Contains(text, "1.2.3") || !strings.Contains(text, "/iquops-package-version") {
					t.Fatal("包构建上下文未隔离材料或未锁定版本")
				}
			}
		}
		if count != 1 {
			t.Fatal("包构建上下文只需要 Dockerfile")
		}
		cfg["version"] = "latest"
		if _, err := validateContainerSource(cfg); err == nil {
			t.Fatal("接受了浮动包版本")
		}
	}
}

func TestStatefulServiceSpecAndObservedPorts(t *testing.T) {
	swarm := domain.Object{"ports": []domain.Object{{"name": "web", "protocol": "http", "targetPort": 3000, "publishedPort": 3001}, {"name": "ssh", "protocol": "tcp", "targetPort": 22, "publishedPort": 2222}}, "volumes": []domain.Object{{"name": "data", "target": "/data"}}}
	svc := domain.Object{"slotKey": "main", "serviceName": "service-one", "replicas": 1}
	spec, err := containerServiceSpec(swarm, svc, "app_example", "network_id", "overlay", "node_example", domain.Object{"image": "example:1"}, map[string]string{"APP": "value"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	task := objectMapFromAny(spec["TaskTemplate"])
	container := objectMapFromAny(task["ContainerSpec"])
	if objectMapFromAny(spec["UpdateConfig"])["Order"] != "stop-first" || objectSlice(container["Mounts"])[0]["Source"] != "iquops-app_example-data" {
		t.Fatal("卷与单写更新配置不正确")
	}
	ports := observedServicePorts(objectMapFromAny(spec["Labels"]), objectMapFromAny(spec["EndpointSpec"]))
	if len(ports) != 2 || ports[0]["name"] != "ssh" || ports[0]["publishedPort"] != 2222 || ports[1]["protocol"] != "http" {
		t.Fatal("服务端口协议或映射不符")
	}
	if err := checkVolumePlacement(domain.Object{"Spec": spec}, "different"); err == nil {
		t.Fatal("有数据卷时不可静默改节点")
	}
	swarm["services"] = []domain.Object{{"slotKey": "main", "replicas": 2}}
	if err := validateContainerRuntime(swarm); err == nil {
		t.Fatal("本地卷不可用于多个写副本")
	}
	b, _ := json.Marshal(spec)
	if bytes.Contains(b, []byte("/var/run/docker.sock")) {
		t.Fatal("宿主套接字不应挂载到应用")
	}
}

func TestContainerStartupKeepsImageEntrypointUnlessScriptOverridesIt(t *testing.T) {
	svc := domain.Object{"slotKey": "main", "serviceName": "example"}
	for _, tc := range []struct {
		config        domain.Object
		command, args string
	}{
		{domain.Object{}, "null", "null"},
		{domain.Object{"command": []string{"--value", "two words"}}, "null", `["--value","two words"]`},
		{domain.Object{"command": []string{"/bin/sh", "-c", "true | cat"}, "shellEntrypoint": true}, `["/bin/sh"]`, `["-c","true | cat"]`},
	} {
		spec, err := containerServiceSpec(tc.config, svc, "app_example", "network", "overlay", "node", domain.Object{"image": "example:1"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		container := objectMapFromAny(objectMapFromAny(spec["TaskTemplate"])["ContainerSpec"])
		command, _ := json.Marshal(container["Command"])
		args, _ := json.Marshal(container["Args"])
		if string(command) != tc.command || string(args) != tc.args {
			t.Fatal("服务启动参数与预期不符")
		}
	}
	if err := validateContainerRuntime(domain.Object{"shellEntrypoint": true, "command": []string{"run"}}); err == nil {
		t.Fatal("允许无解释器的脚本覆盖")
	}
	if err := normalizeApplicationSwarm(domain.EmptyState(), "project", domain.Object{"stackName": "retired"}); err == nil {
		t.Fatal("允许写入已移除的栈名称")
	}
}

func TestImageReleaseQueuesWithoutJenkinsAndFreezesInputs(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	app, err := r.CreateObject(ctx, "applications", "app", domain.Object{"projectId": domain.BuiltinProjectID, "name": "示例"})
	if err != nil {
		t.Fatal(err)
	}
	id := stringValue(app["id"])
	err = store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "server_one", "health": domain.Object{"metrics": domain.Object{"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "swarmVerificationStatus": "verified", "swarmClusterId": "cluster_example", "swarmNodeId": "node_example", "traefik": true}}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := domain.Object{"sourceConfig": domain.Object{"provider": "image", "image": "sample:1.0"}, "deploymentManagerServerId": "server_one", "swarmRuntime": domain.Object{"services": []domain.Object{{"serverId": "server_one", "slotKey": "main", "serviceName": "sample-main", "replicas": 1}}}}
	if _, err = r.UpsertApplicationConfig(ctx, id, cfg); err != nil {
		t.Fatal(err)
	}
	tk, err := r.CreateApplicationReleaseOperation(ctx, id, "")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := store.LoadState(ctx)
	plan := findObject(state.ReleasePlans, stringValue(tk.Payload["releasePlanId"]))
	if tk.Action != "deploy_application" || tk.TargetID != id || stringValue(objectMapFromAny(objectMapFromAny(plan["buildSnapshot"])["sourceSnapshot"])["image"]) != "sample:1.0" {
		t.Fatal("应用镜像未使用独立确认和发布快照")
	}
}

func TestPinImageDigestByRequestedRepository(t *testing.T) {
	first := "sha256:" + strings.Repeat("a", 64)
	second := "sha256:" + strings.Repeat("b", 64)
	digests := []string{"unrelated@" + first, "docker.io/library/postgres@" + second, "registry.example.invalid:5000/tools/demo@" + first}
	if registryRepository("index.registry.example.invalid/tools/demo:1") == registryRepository("registry.example.invalid/tools/demo:1") {
		t.Fatal("自建仓库的域名必须完全匹配")
	}
	if got := matchingImageDigest("index.docker.io/postgres:16", digests); got != digests[1] {
		t.Fatal("Docker Hub 的公开域名应指向同一仓库")
	}
	if got := matchingImageDigest("postgres:16", digests); got != digests[1] {
		t.Fatal("错误使用了另一个仓库的摘要")
	}
	if got := matchingImageDigest("postgres@"+first, digests); got != "" {
		t.Fatal("接受了不同的仓库摘要")
	}
	if got := matchingImageDigest("registry.example.invalid:5000/tools/demo:1", digests); got != digests[2] {
		t.Fatal("镜像名中的仓库端口不应被当作标签")
	}
}

func TestGitReadbackBindsArtifactToReleaseAndSlot(t *testing.T) {
	plan := domain.Object{"applicationId": "app_demo", "buildSnapshot": domain.Object{"swarmRuntime": domain.Object{"services": []domain.Object{
		{"slotKey": "web", "serverId": "server_one"},
	}}}}
	config := "sha256:" + strings.Repeat("a", 64)
	manifest := "sha256:" + strings.Repeat("b", 64)
	fact := domain.Object{"serviceSlotKey": "web", "serverId": "server_one", "image": "iquops/app_demo:run_demo-0-arm64", "platform": "linux/arm64", "imageDigest": manifest, "configDigest": config, "manifestDigest": manifest}
	if _, err := verifiedGitArtifactFacts([]domain.Object{fact}, plan, "run_demo"); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"serviceSlotKey": "worker", "serverId": "server_two", "image": "iquops/app_demo:other-0-arm64", "platform": "linux/amd64", "imageDigest": "sha256:" + strings.Repeat("c", 64)} {
		invalid := cloneObject(fact)
		invalid[key] = value
		if _, err := verifiedGitArtifactFacts([]domain.Object{invalid}, plan, "run_demo"); err == nil {
			t.Fatalf("接受了不匹配的制品字段 %s", key)
		}
	}
}

package application

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestImagePullUsesApplicationMirrorAndVerifiesDigest(t *testing.T) {
	const mirror = "https://mirror.example.test:8443/cache"
	const ref = "mirror.example.test:8443/cache/library/alpine"
	digest := "sha256:" + strings.Repeat("a", 64)
	client, _, _ := runtimeProbeSSH(t, func(cmd string) (string, uint32) {
		if !strings.Contains(cmd, "docker pull '"+ref+":3.21'") || !strings.Contains(cmd, "docker image inspect '"+ref+":3.21'") {
			return "", 1
		}
		return digest + "|linux/arm64|[\"" + ref + "@" + digest + "\"]", 0
	})
	facts, err := prepareContainerArtifact(context.Background(), containerPeer{client: client}, domain.Object{"provider": "image", "registryMirrorUrl": mirror}, "release", "main", "alpine:3.21", nil)
	if err != nil || facts["image"] != ref+"@"+digest || facts["baseImageDigest"] != "docker.io/library/alpine@"+digest {
		t.Fatal("镜像未从覆盖源按摘要固定", facts, err)
	}
	for _, item := range []struct{ image, override, want string }{
		{"docker.io/alpine@" + digest, mirror, ref + "@" + digest},
		{"other.example.test/repo/app:1", mirror, "other.example.test/repo/app:1"},
		{"alpine:3.21", "", "alpine:3.21"},
		{"org/app:1", "https://mirror", "mirror:443/org/app:1"},
	} {
		got, err := imagePullReference(item.image, item.override)
		if err != nil || got != item.want {
			t.Fatal(item, got, err)
		}
	}
	for _, address := range []string{"http://mirror.example.test", "https://user@mirror.example.test", "https://mirror.example.test?key=value", "https://mirror.example.test/a/../b", "https://mirror.example.test:65536", "https://mirror.example.test:0", "https://mirror.example.test/#"} {
		if _, err := validateApplicationSourceConfig(domain.Object{"provider": "image", "image": "alpine:3.21", "registryMirrorUrl": address}); err == nil {
			t.Fatal("接受无效覆盖源", address)
		}
	}
}

func TestGitMirrorIsFrozenForRelease(t *testing.T) {
	const inherited = "https://server.example.test"
	const override = "https://app.example.test"
	ctx := context.Background()
	runtime, _, _ := seededJenkinsExecution(t)
	state, err := runtime.Store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	server := state.Servers[0]
	metrics := serverRuntimeMetrics(server)
	metrics["jenkinsBundleVersion"] = "v13-jenkins-2.568.1"
	metrics["dockerRegistryMirrors"] = []string{inherited}
	source := objectMapFromAny(state.ApplicationDeploymentConfigs[0]["sourceConfig"])
	const explicit = "https://server.example.test:8443/cache"
	profile := domain.Object{}
	server["platformProfile"] = profile
	for _, tc := range []struct{ app, server, want, origin string }{
		{"", "", inherited, "daemon"}, {"", explicit, explicit, "server"}, {override, explicit, override, "application"},
	} {
		metrics["dockerRegistryMirrors"] = []string{inherited}
		profile["dockerRegistryMirrorUrl"] = tc.server
		source["registryMirrorUrl"] = tc.app
		plan, err := newApplicationReleasePlan(&state, "app_demo", nil)
		if err != nil {
			t.Fatal(err)
		}
		metrics["dockerRegistryMirrors"] = []string{"https://changed.example.test"}
		profile["dockerRegistryMirrorUrl"] = "https://changed.example.test:8443"
		source["registryMirrorUrl"] = "https://changed.example.test"
		materials, err := runtime.trustedJenkinsMaterials(ctx, state, plan)
		if err != nil || !slices.Equal(objectStringSlice(materials["registryMirrors"]), []string{tc.want}) {
			t.Fatal("镜像源随当前配置变化", err)
		}
		pull := objectMapFromAny(materials["buildPull"])
		if pull["origin"] != tc.origin || (tc.origin != "daemon" && pull["pullMirror"] != tc.want) {
			t.Fatal("执行镜像继承错误", pull)
		}
		facts := objectSlice(publicApplicationBuild(plan, nil)["pullMirrors"])
		if len(facts) != 1 || facts[0]["role"] != "buildGateway" || facts[0]["origin"] != tc.origin || !slices.Equal(objectStringSlice(facts[0]["addresses"]), []string{tc.want}) {
			t.Fatal("历史镜像源错误", facts)
		}
	}
}

func TestImageMirrorSnapshotsKeepNodeConfiguration(t *testing.T) {
	state := domain.State{Servers: []domain.Object{
		{"id": "one", "platformProfile": domain.Object{"dockerRegistryMirrorUrl": "http://mirror.example.test:5000"}},
		{"id": "two", "platformProfile": domain.Object{"dockerRegistryMirrorUrl": "https://mirror.example.test:8443/cache"}},
	}}
	source := domain.Object{"provider": "image", "image": "alpine:3.21"}
	config := domain.Object{"sourceConfig": source, "swarmRuntime": domain.Object{"services": []domain.Object{{"serverId": "one"}, {"serverId": "two"}}}}
	frozen, err := snapshotNodePullMirrors(state, config)
	if err != nil {
		t.Fatal(err)
	}
	one, two := objectMapFromAny(frozen["one"]), objectMapFromAny(frozen["two"])
	if one["verifyDaemon"] != true || one["pullMirror"] != "" || two["pullMirror"] != "https://mirror.example.test:8443/cache" {
		t.Fatal(one, two)
	}
	source["image"] = "private.example.test/image:3"
	frozen, err = snapshotNodePullMirrors(state, config)
	if err != nil || len(frozen) != 0 {
		t.Fatal("独立仓库被镜像源改变", frozen, err)
	}
}

func TestFirstDaemonReadIsRetainedAcrossRetries(t *testing.T) {
	ctx := context.Background()
	runtime, _, _ := seededJenkinsExecution(t)
	plan, err := runtime.CreateReleasePlan(ctx, "app_demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := stringValue(plan["id"])
	actual := []string{"http://daemon.example.test:5000"}
	if err := runtime.pinFirstDaemonMirrors(ctx, id, "server_demo", actual); err != nil {
		t.Fatal(err)
	}
	if err := runtime.pinFirstDaemonMirrors(ctx, id, "server_demo", actual); err != nil {
		t.Fatal(err)
	}
	if err := runtime.pinFirstDaemonMirrors(ctx, id, "server_demo", []string{}); err == nil {
		t.Fatal("接受了与首次观测不一致的镜像源")
	}
	state, err := runtime.Store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	facts := objectSlice(publicApplicationBuild(findObject(state.ReleasePlans, id), nil)["pullMirrors"])
	if len(facts) != 1 || facts[0]["probed"] != true || !slices.Equal(objectStringSlice(facts[0]["addresses"]), actual) {
		t.Fatal("构建记录未保留首次观测", facts)
	}
}

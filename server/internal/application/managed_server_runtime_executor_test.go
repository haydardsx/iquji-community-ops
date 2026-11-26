package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestLoadJenkinsRuntimeBundleContainsOnlyCompleteManagedRuntime(t *testing.T) {
	root := filepath.Join("..", "..", "resources", "runtime", "managed", "jenkins")
	bundle, err := loadJenkinsRuntimeBundle(root)
	if err != nil {
		t.Fatalf("load Jenkins runtime bundle: %v", err)
	}

	files := map[string]string{}
	for _, file := range bundle {
		files[file.RelativePath] = string(file.Content)
	}
	for _, required := range []string{
		"Dockerfile",
		"plugins.txt",
		"plugins.lock",
		"run-release.mjs",
		"broker-state.mjs",
		"init.groovy.d/01-managed-security.groovy",
		"init.groovy.d/02-seed-release-jobs.groovy",
	} {
		if _, ok := files[required]; !ok {
			t.Fatalf("managed Jenkins bundle missing %s", required)
		}
	}
	if !strings.Contains(files["Dockerfile"], "\nUSER jenkins\n") {
		t.Fatal("managed Jenkins image is missing its unprivileged runtime user")
	}
	for _, required := range []string{"AS broker", "AS controller", "library/", "jenkins-plugins.tar /usr/share/jenkins/ref/plugins/", "sha256sum --strict -c"} {
		if !strings.Contains(files["Dockerfile"], required) {
			t.Fatalf("受管运行镜像缺少 %s", required)
		}
	}
	security := files["init.groovy.d/01-managed-security.groovy"]
	for _, required := range []string{"HudsonPrivateSecurityRealm(false)", "new ProjectMatrixAuthorizationStrategy()", "authorization.add(Jenkins.READ, 'iquops-viewer')", "DefaultCrumbIssuer(true)", "setNumExecutors(0)"} {
		if !strings.Contains(security, required) {
			t.Fatalf("控制器缺少安全配置 %s", required)
		}
	}
	library := files["library/vars/iquopsPipeline.groovy"]
	if strings.Contains(library, "sh(") || !strings.Contains(library, "CpsThread.current()?.execution") || strings.Contains(library, "releaseRunId(currentBuild.rawBuild)") {
		t.Fatal("可信库必须按真实构建身份调用隔离执行器")
	}
	if !strings.Contains(files["run-release.mjs"], "iquops-runtime-health.json") ||
		!strings.Contains(files["init.groovy.d/02-seed-release-jobs.groovy"], "allowVersionOverride") {
		t.Fatal("Jenkins 健康检查必须核验可信库、沙箱及控制器安全设置")
	}
}

func TestLoadJenkinsRuntimeBundleExcludesUndeclaredFiles(t *testing.T) {
	source := filepath.Join("..", "..", "resources", "runtime", "managed", "jenkins")
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(source)); err != nil {
		t.Fatal("could not copy the runtime fixture")
	}
	const extra = "local-note.txt"
	if err := os.WriteFile(filepath.Join(root, extra), []byte("sample"), 0o600); err != nil {
		t.Fatal("could not add the unrelated fixture")
	}
	bundle, err := loadJenkinsRuntimeBundle(root)
	if err != nil {
		t.Fatal("could not load the managed runtime")
	}
	for _, item := range bundle {
		if item.RelativePath == extra {
			t.Fatal("an undeclared file was included in the runtime bundle")
		}
	}
}

func TestLoadJenkinsRuntimeBundleRejectsIncompleteBundle(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatalf("write partial bundle: %v", err)
	}

	_, err := loadJenkinsRuntimeBundle(root)
	if err == nil || !strings.HasPrefix(err.Error(), "JENKINS_BUNDLE_INCOMPLETE:") {
		t.Fatalf("incomplete bundle error = %v", err)
	}
	for _, missing := range []string{
		"plugins.txt",
		"plugins.lock",
		"run-release.mjs",
		"init.groovy.d/01-managed-security.groovy",
		"init.groovy.d/02-seed-release-jobs.groovy",
	} {
		if !strings.Contains(err.Error(), missing) {
			t.Fatalf("incomplete bundle error should name %s: %v", missing, err)
		}
	}
}

func TestMonitoringAgentInstallAndUninstallShareRemoteDirectory(t *testing.T) {
	install := monitoringAgentInstallDirectoryCommand()
	unit := monitoringAgentUnit()
	uninstall := monitoringAgentUninstallCommand()
	if !strings.Contains(install, monitoringAgentRemoteDir) {
		t.Fatal("agent install directory is missing from the setup command")
	}
	if !strings.Contains(install, monitoringAgentLegacyRemoteDir) {
		t.Fatal("agent install must clear the migrated directory")
	}
	if !strings.Contains(unit, "ExecStart="+monitoringAgentBinaryPath) {
		t.Fatal("agent service uses a different binary path")
	}
	if !strings.Contains(uninstall, monitoringAgentRemoteDir) || !strings.Contains(uninstall, monitoringAgentLegacyRemoteDir) {
		t.Fatal("agent uninstall must remove the active and migrated directories")
	}
}

func TestJenkinsDockerRunCommandIsLoopbackOnlyAndDropsDirectRootPrivileges(t *testing.T) {
	command := jenkinsDockerRunCommand(ServerRuntimeExecutionInput{
		JenkinsImage:         "iquops-managed-jenkins:v6-jenkins-2.568.1",
		JenkinsContainer:     "iquops-jenkins",
		JenkinsBundleVersion: "v6-jenkins-2.568.1",
		JenkinsPort:          58080,
	})

	for _, required := range []string{
		"--network host",
		"--security-opt no-new-privileges:true",
		"--cap-drop ALL",
		"JENKINS_OPTS=--httpPort=58080 --httpListenAddress=127.0.0.1",
		"/opt/iquops/jenkins/home:/var/jenkins_home",
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("Jenkins run command missing %q", required)
		}
	}
	for _, forbidden := range []string{"--publish", "--user root", "0.0.0.0", "docker.sock", "DOCKER_HOST", "--group-add"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("Jenkins run command must not contain %q", forbidden)
		}
	}
}

func TestMergeDockerRegistryMirrorConfigPreservesExistingDaemonSettings(t *testing.T) {
	current := `{
  "log-driver": "json-file",
  "registry-mirrors": ["https://existing.example.com/"]
}`
	next, changed, err := mergeDockerRegistryMirrorConfig(current, "https://mirror.example.test")
	if err != nil || !changed {
		t.Fatalf("merge Docker registry mirror: changed=%v err=%v", changed, err)
	}
	var config map[string]any
	if err := json.Unmarshal(next, &config); err != nil {
		t.Fatalf("decode merged daemon config: %v", err)
	}
	if config["log-driver"] != "json-file" {
		t.Fatal("existing daemon setting was lost")
	}
	mirrors, ok := config["registry-mirrors"].([]any)
	if !ok || len(mirrors) != 2 || mirrors[0] != "https://mirror.example.test" || mirrors[1] != "https://existing.example.com/" {
		t.Fatal("merged registry mirror list differs from the expected list")
	}
}

func TestMergeDockerRegistryMirrorConfigAvoidsAnUnnecessaryDaemonRestart(t *testing.T) {
	next, changed, err := mergeDockerRegistryMirrorConfig(
		`{"registry-mirrors":["https://mirror.example.test/"]}`,
		"https://mirror.example.test",
	)
	if err != nil || changed || next != nil {
		t.Fatal("existing registry mirror should be unchanged")
	}
}

func TestMergeDockerRegistryMirrorConfigRejectsInvalidExistingConfig(t *testing.T) {
	for _, current := range []string{
		`{"registry-mirrors":"https://mirror.example.com"}`,
		`{"registry-mirrors":[42]}`,
		`not-json`,
	} {
		if _, _, err := mergeDockerRegistryMirrorConfig(current, "https://mirror.example.test"); err == nil || err.Error() != "DOCKER_DAEMON_CONFIG_INVALID" {
			t.Fatal("invalid daemon configuration must return the public validation error")
		}
	}
}

func TestDesiredDockerRegistryMirrorURLRejectsUnsafeValues(t *testing.T) {
	withUserInfo := url.URL{Scheme: "https", Host: "mirror.example.test", User: url.UserPassword("example", "sample")}
	for _, value := range []string{
		"file:///tmp/registry",
		withUserInfo.String(),
		"https://mirror.example.test?token=example",
	} {
		_, err := desiredDockerRegistryMirrorURL(domain.Object{
			"platformProfile": domain.Object{"dockerRegistryMirrorUrl": value},
		})
		if err == nil || err.Error() != "DOCKER_REGISTRY_MIRROR_URL_INVALID" {
			t.Fatal("unsafe registry mirror must return the public validation error")
		}
	}
}

func TestServerMirrorRejectsNewCredentialsAndHidesLegacyValues(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	withUserInfo := url.URL{Scheme: "https", Host: "mirror.example.test", User: url.UserPassword("example", "sample")}
	profile := domain.Object{"dockerRegistryMirrorUrl": withUserInfo.String(), "architecture": "arm64"}
	if _, err := runtime.CreateObject(ctx, "servers", "srv", domain.Object{"platformProfile": profile}); err == nil {
		t.Fatal("server creation accepted credentials in the registry mirror")
	}
	const id = "example_server"
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = append(state.Servers, domain.Object{"id": id, "platformProfile": profile})
		return nil
	}); err != nil {
		t.Fatal("could not seed a legacy server profile")
	}
	items, err := runtime.ListObjects(ctx, "servers")
	if err != nil {
		t.Fatal("could not list servers")
	}
	server := findObject(objectSlice(items["items"]), id)
	visible := objectMapFromAny(server["platformProfile"])
	if visible["architecture"] != "arm64" || visible["dockerRegistryMirrorUrl"] != nil {
		t.Fatal("the server response must omit a credential-bearing registry mirror")
	}
	edited, err := runtime.UpdateObject(ctx, "servers", id, domain.Object{"name": "Updated server"})
	if err != nil || objectMapFromAny(edited["platformProfile"])["dockerRegistryMirrorUrl"] != nil {
		t.Fatal("updating other server fields must keep the legacy mirror private")
	}
	if _, err = runtime.UpdateObject(ctx, "servers", id, domain.Object{"platformProfile": profile}); err == nil {
		t.Fatal("server updates accepted credentials in the registry mirror")
	}
	valid := domain.Object{"dockerRegistryMirrorUrl": "https://mirror.example.test"}
	if _, err = runtime.UpdateObject(ctx, "servers", id, domain.Object{"platformProfile": valid}); err != nil {
		t.Fatal("could not replace an invalid legacy mirror")
	}
}

func TestDockerRegistryMirrorApplyCommandValidatesAndRollsBack(t *testing.T) {
	command := dockerRegistryMirrorApplyCommand("/etc/docker/daemon.json.iquops.tmp")
	for _, required := range []string{
		"dockerd --validate",
		"cp -p \"$config\" \"$backup\"",
		"systemctl restart docker",
		"mv -f \"$backup\" \"$config\"",
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("registry mirror apply command missing %q", required)
		}
	}
}

func TestStableJenkinsImageBuildErrorClassifiesSafeFailureStages(t *testing.T) {
	tests := map[string]string{
		`failed to resolve source metadata for docker.io/jenkins/jenkins: dial tcp: i/o timeout`:                "JENKINS_BASE_IMAGE_PULL_FAILED",
		`RUN apt-get update returned a non-zero code: 100`:                                                      "JENKINS_SYSTEM_PACKAGE_INSTALL_FAILED",
		`RUN jenkins-plugin-cli --plugin-file plugins.txt: exit status 1`:                                       "JENKINS_PLUGIN_INSTALL_FAILED",
		`remote build secret=value failed: exit status 1`:                                                       "JENKINS_IMAGE_BUILD_FAILED",
		"#2 RUN jenkins-plugin-cli --plugin-file plugins.txt\n#2 CACHED\n#3 RUN apt-get update: exit code: 100": "JENKINS_SYSTEM_PACKAGE_INSTALL_FAILED",
		"#2 RUN apt-get update\n#2 CACHED\n#3 ERROR: failed to fetch anonymous token":                           "JENKINS_BASE_IMAGE_PULL_FAILED",
		"#2 RUN jenkins-plugin-cli --plugin-file plugins.txt\n#2 DONE\n#3 RUN git init: exit code: 1":           "JENKINS_IMAGE_BUILD_FAILED",
	}
	for message, expected := range tests {
		if code := stableJenkinsImageBuildError(errors.New(message)); code != expected {
			t.Fatalf("expected Jenkins build failure category %s", expected)
		}
	}
	for err, expected := range map[error]string{
		context.Canceled:         "OPERATION_CANCELLED",
		context.DeadlineExceeded: "OPERATION_TIMEOUT",
	} {
		if got := stableJenkinsImageBuildError(err); got != expected {
			t.Errorf("构建中断时应返回 %s，实际为 %s", expected, got)
		}
	}
}

func TestStableJenkinsHealthErrorClassifiesSafeInitializationFailures(t *testing.T) {
	tests := []struct {
		logs      string
		rootReady bool
		expected  string
	}{
		{logs: `cp: cannot open '/usr/share/jenkins/ref/init.groovy.d/seed.groovy': Permission denied`, rootReady: true, expected: "JENKINS_RUNTIME_PERMISSION_FAILED"},
		{logs: `SEVERE Failed Loading plugin Pipeline`, rootReady: true, expected: "JENKINS_PLUGIN_INITIALIZATION_FAILED"},
		{logs: `Jenkins is fully up and running`, rootReady: true, expected: "JENKINS_MANAGED_JOB_MISSING"},
		{logs: `Jenkins failed before HTTP startup`, rootReady: false, expected: "JENKINS_HEALTHCHECK_FAILED"},
	}
	for _, test := range tests {
		if code := stableJenkinsHealthError(test.logs, test.rootReady); code != test.expected {
			t.Fatalf("expected Jenkins health failure category %s", test.expected)
		}
	}
}

func TestInterruptibleRemoteCommandTerminatesItsChildOnWorkerShutdown(t *testing.T) {
	command := interruptibleRemoteCommand("docker build /opt/iquops/jenkins/runtime")
	for _, required := range []string{
		"trap cleanup HUP INT TERM",
		"kill -TERM \"$child_pid\"",
		"(docker build /opt/iquops/jenkins/runtime) &",
		"wait \"$child_pid\"",
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("interruptible remote command missing %q", required)
		}
	}
}

func TestValidateSwarmClusterIDsRejectsAWorkerFromAnotherCluster(t *testing.T) {
	if err := validateSwarmClusterIDs("cluster-a", "cluster-a"); err != nil {
		t.Fatalf("matching cluster IDs should pass: %v", err)
	}
	if err := validateSwarmClusterIDs("", "cluster-a"); err != nil {
		t.Fatalf("a manager-verified worker without a locally exposed cluster ID should pass: %v", err)
	}
	if err := validateSwarmClusterIDs("cluster-b", "cluster-a"); err == nil || err.Error() != "SWARM_CLUSTER_MISMATCH" {
		t.Fatalf("mismatched cluster IDs error = %v", err)
	}
	if err := validateSwarmClusterIDs("cluster-a", ""); err == nil || err.Error() != "SWARM_MANAGER_CLUSTER_ID_UNAVAILABLE" {
		t.Fatalf("missing manager cluster ID error = %v", err)
	}
}

func TestSwarmNodeLabelCommandUsesTheProbedNodeID(t *testing.T) {
	command := swarmNodeLabelCommand("srv_manager", "node_123")
	if command != "docker node update --label-add 'iquops.server-id=srv_manager' 'node_123'" {
		t.Fatal("Swarm node label command used the wrong node or label")
	}
	if strings.HasSuffix(command, " self") {
		t.Fatal("Swarm node label command must not use the unsupported self alias")
	}
}

func TestStableSwarmProbeErrorDoesNotExposeRemoteCommandDetails(t *testing.T) {
	err := errors.New("docker node inspect node-secret: exit status 1")
	if code := stableSwarmProbeError(err); code != "SWARM_CLUSTER_UNVERIFIED" {
		t.Fatalf("stable Swarm probe code = %q", code)
	}
	if code := stableSwarmProbeError(errors.New("wrapped: SWARM_CLUSTER_MISMATCH")); code != "SWARM_CLUSTER_MISMATCH" {
		t.Fatalf("known Swarm probe code = %q", code)
	}
}

func TestStableTraefikExecutionErrorDoesNotExposeRemoteCommandDetails(t *testing.T) {
	err := errors.New("docker service create --secret=value: exit status 1")
	if code := stableTraefikExecutionError(err, "REMOTE_TRAEFIK_INSTALL_FAILED"); code != "REMOTE_TRAEFIK_INSTALL_FAILED" {
		t.Fatalf("stable Traefik fallback code = %q", code)
	}
	if code := stableTraefikExecutionError(errors.New("TRAEFIK_SWARM_NETWORK_INVALID: exit status 5"), "REMOTE_TRAEFIK_INSTALL_FAILED"); code != "TRAEFIK_SWARM_NETWORK_INVALID" {
		t.Fatalf("known Traefik code = %q", code)
	}
}

func TestManagedSwarmNetworkAndServiceProbeHelpers(t *testing.T) {
	if !managedOverlayNetworkReady("overlay|swarm") {
		t.Fatal("managed overlay network should be ready")
	}
	for _, invalid := range []string{"bridge|local", "overlay|local", "overlay", ""} {
		if managedOverlayNetworkReady(invalid) {
			t.Fatalf("network %q must not be treated as a managed Swarm overlay", invalid)
		}
	}

	networkID, ready := managedOverlayNetworkID("network-id|overlay|swarm", nil)
	if !ready || networkID != "network-id" {
		t.Fatalf("managed overlay network = %q, %v", networkID, ready)
	}
	if _, ready := managedOverlayNetworkID("network-id|bridge|local", nil); ready {
		t.Fatal("bridge network must not pass the Traefik probe")
	}
	if !serviceUsesNetwork("other-network\nnetwork-id\n", networkID) {
		t.Fatal("Traefik service network attachment should be detected")
	}
	if serviceUsesNetwork("network-id-prefix", networkID) {
		t.Fatal("Traefik service network attachment must require an exact network ID")
	}
	if !serviceReplicasReady("iquops-traefik|1/1", "iquops-traefik") {
		t.Fatal("Traefik service with all desired replicas should be ready")
	}
	if serviceReplicasReady("iquops-traefik|0/1", "iquops-traefik") {
		t.Fatal("Traefik service without a running replica must not be ready")
	}

	ensureCommand := swarmOverlayNetworkEnsureCommand("iquops-public")
	for _, required := range []string{
		"--format '{{.Driver}}|{{.Scope}}'",
		"docker network create --driver overlay --attachable --label 'iquops.owner=iquops' --label 'iquops.component=swarm_network' 'iquops-public'",
		`[ "$network_spec" != 'overlay|swarm' ]`,
	} {
		if !strings.Contains(ensureCommand, required) {
			t.Fatal("Swarm overlay setup lacks a required validation or creation step")
		}
	}
}

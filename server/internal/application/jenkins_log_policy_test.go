package application

import (
	"context"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

func TestLegacyJenkinsRemainsReadyWithNewLoggingBundleDefault(t *testing.T) {
	client, server, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch {
		case command == "docker container ls -a --format '{{.Names}}'":
			return "iquops-jenkins", 0
		case strings.HasPrefix(command, "docker inspect -f "):
			return "true|iquops-managed-jenkins:v8-jenkins-2.568.1|iquops|jenkins|v8-jenkins-2.568.1", 0
		case strings.Contains(command, "broker-cli.mjs") && strings.Contains(command, "health"):
			return `{"status":"ready"}`, 0
		case strings.Contains(command, "jenkins.war --version"):
			return "2.568.1", 0
		default:
			t.Fatal("探测旧运行包发出了意外命令")
			return "", 1
		}
	})
	input := withManagedRuntimeDefaults(ServerRuntimeExecutionInput{Server: server, SSHKey: key})
	if input.JenkinsBundleVersion != platform.ManagedJenkinsBundleVersion {
		t.Fatal("测试没有使用新运行包默认值")
	}
	result := domain.Object{}
	applyRemoteJenkinsProbe(context.Background(), client, input, result)
	metrics := objectMapFromAny(result["metrics"])
	if metrics["jenkins"] != true || metrics["jenkinsStatus"] != "running" || metrics["jenkinsBundleVersion"] != "v8-jenkins-2.568.1" {
		t.Fatal("仅默认版本变化就阻止了健康旧实例构建")
	}
}

func TestOlderBrokersCannotReceiveDeploymentMaterials(t *testing.T) {
	runtime, _, ctx := newTaskTestRuntime(t)
	for _, version := range []string{"", "v8-jenkins-2.568.1", "v11-jenkins-2.568.1"} {
		state := domain.EmptyState()
		state.Servers = []domain.Object{readinessServer("build", domain.Object{"jenkinsBundleVersion": version})}
		plan := domain.Object{"buildServerId": "build", "deploymentManagerServerId": "build"}
		if _, err := runtime.trustedJenkinsMaterials(ctx, state, plan); err == nil || err.Error() != "JENKINS_APPLICATION_BUNDLE_UPDATE_REQUIRED" {
			t.Fatalf("未阻止旧版运行包下发材料 %q: %v", version, err)
		}
	}
}

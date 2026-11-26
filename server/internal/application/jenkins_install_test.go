package application

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestJenkinsHealthProbeImageIsPinnedToManagedDigest(t *testing.T) {
	if !strings.HasPrefix(jenkinsHealthProbeImage, "curlimages/curl@sha256:") {
		t.Fatalf("健康检查镜像必须使用摘要引用: %q", jenkinsHealthProbeImage)
	}
	if strings.Contains(jenkinsHealthProbeImage, "8.12.1") {
		t.Fatalf("健康检查镜像不能回到已失效的旧版本: %q", jenkinsHealthProbeImage)
	}
	if got := shellQuote(jenkinsHealthProbeImage); got != "'curlimages/curl@sha256:3fb7e13e5d179a845c97d78204f3f1064d1907037f26a3ebf91710571151fd93'" {
		t.Fatalf("健康检查镜像命令未安全引用: %q", got)
	}
}

func TestJenkinsBrokerProbeAndLaunchUseMatchingHostAccess(t *testing.T) {
	input := ServerRuntimeExecutionInput{JenkinsImage: "example/jenkins:test", JenkinsContainer: "example-jenkins"}
	// 用 shell 展开和真实启动一致的参数，检查预检先于任何持久容器操作。
	script := "docker() { printf '%s\\n' \"$@\"; }\n" + jenkinsBrokerPreflightCommand(input.JenkinsImage+"-broker")
	out, err := exec.Command("sh", "-e", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	probe := strings.Split(strings.TrimSpace(string(out)), "\n")
	if got := strings.Join(probe, " "); got != "run --rm --network host --cap-add NET_ADMIN --mount type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock --entrypoint node example/jenkins:test-broker /opt/iquops/runtime/broker-firewall.mjs" {
		t.Fatalf("镜像预检参数有误 %q", probe)
	}
	for _, binary := range []string{"iptables-legacy", "iptables-nft"} {
		selected, err := jenkinsBrokerPreflightResult("Docker warning\n"+binary, nil)
		if selected != binary || err != nil {
			t.Fatalf("防火墙后端有误 %q: %v", selected, err)
		}
		command := "docker() { printf '%s\\n' \"$@\"; }\n" + jenkinsBrokerRunCommand(input, selected)
		out, err := exec.Command("sh", "-e", "-c", command).Output()
		if err != nil || !strings.Contains(string(out), "\n--env\nIQUOPS_IPTABLES_BIN="+binary+"\n") {
			t.Fatalf("隔离执行器未获得所选后端 %q: %v", out, err)
		}
	}
}

func TestJenkinsBrokerStartDiagnostics(t *testing.T) {
	checks := []struct {
		output string
		err    error
		want   string
	}{
		{"", nil, "JENKINS_BROKER_FIREWALL_UNAVAILABLE"},
		{"unexpected", nil, "JENKINS_BROKER_FIREWALL_UNAVAILABLE"},
		{"JENKINS_BROKER_FIREWALL_UNAVAILABLE", errors.New("exit status 1"), "JENKINS_BROKER_FIREWALL_UNAVAILABLE"},
		{"JENKINS_BROKER_DOCKER_SOCKET_UNAVAILABLE", errors.New("exit status 1"), "JENKINS_BROKER_DOCKER_SOCKET_UNAVAILABLE"},
		{"", errors.New("invalid mount config: bind source path does not exist: /var/run/docker.sock"), "JENKINS_BROKER_DOCKER_SOCKET_UNAVAILABLE"},
		{"", errors.New("authorization denied by plugin"), "JENKINS_BROKER_HOST_ACCESS_DENIED"},
		{"", errors.New("operation not permitted"), "JENKINS_BROKER_HOST_ACCESS_DENIED"},
		{"", errors.New("Conflict. The container name is already in use"), "JENKINS_CONTAINER_NAME_CONFLICT"},
		{"", errors.New("unclassified failure"), "JENKINS_BROKER_START_FAILED"},
		{"", context.Canceled, context.Canceled.Error()},
	}
	for _, check := range checks {
		backend, err := jenkinsBrokerPreflightResult(check.output, check.err)
		if backend != "" || err == nil || err.Error() != check.want {
			t.Fatalf("预检诊断有误 %q, %v；预期 %q", backend, err, check.want)
		}
	}
}

func TestJenkinsReplacementRequiresOwnedID(t *testing.T) {
	first, second := strings.Repeat("a", 64), strings.Repeat("b", 64)
	rows := "example|" + first + "|iquops|jenkins\nexample-broker|" + second + "|iquops|jenkins-broker"
	ids, err := ownedJenkinsContainers(rows, "example")
	if err != nil || ids["jenkins"] != first || ids["jenkins-broker"] != second {
		t.Fatalf("未正确识别受管容器 %v, %v", ids, err)
	}
	if removeOwnedJenkinsContainer(ids["jenkins-broker"]) != "docker rm -f '"+second+"' >/dev/null\n" || removeOwnedJenkinsContainer("") != "" {
		t.Fatal("替换操作只能按已核实的完整容器 ID 删除")
	}
	for _, row := range []string{
		"example-broker|" + second + "||", "example|" + first + "|iquops|jenkins-broker",
		"example-broker|" + second + "|other|jenkins-broker", "example|short|iquops|jenkins",
	} {
		ids, err := ownedJenkinsContainers(row, "example")
		if ids != nil || err == nil || err.Error() != "JENKINS_CONTAINER_NAME_CONFLICT" {
			t.Fatalf("必须保留归属不匹配的容器 %q", row)
		}
	}
}

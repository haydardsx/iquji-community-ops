package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const jenkinsHealthProbeImage = "curlimages/curl@sha256:3fb7e13e5d179a845c97d78204f3f1064d1907037f26a3ebf91710571151fd93"

func (e *SystemServerRuntimeExecutor) InstallJenkins(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	input = withManagedRuntimeDefaults(input)
	bundle, err := loadJenkinsRuntimeBundle(input.JenkinsBundleDir)
	if err != nil {
		return nil, err
	}
	input.LogStep("jenkins", "verify-plugins", "info", "校验随包 Jenkins 插件及其依赖")
	archive, digest, err := openJenkinsPluginArchive(input, bundle)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	if _, err := remoteOutput(ctx, client, "docker version --format '{{.Server.Version}}'"); err != nil {
		return nil, errors.New("JENKINS_DOCKER_REQUIRED")
	}
	containers, err := inspectJenkinsContainers(ctx, client, input.JenkinsContainer)
	if err != nil {
		return nil, err
	}
	if err := ensureDockerRegistryMirror(ctx, client, input.Server, input.LogStep); err != nil {
		return nil, err
	}
	input.LogStep("jenkins", "health-probe-image", "info", "预拉取固定健康检查镜像")
	if err := remoteRun(ctx, client, "docker pull "+shellQuote(jenkinsHealthProbeImage)); err != nil {
		return nil, errors.New("JENKINS_HEALTH_PROBE_IMAGE_FAILED")
	}
	if input.JenkinsPort >= 65535 {
		return nil, errors.New("JENKINS_PORT_INVALID")
	}
	bundleRoot := jenkinsRemoteRoot + "/runtime/" + sanitizeRuntimeName(input.JenkinsBundleVersion)
	input.LogStep("jenkins", "upload-runtime", "info", "上传认证控制器与隔离执行器")
	if err := remoteRun(ctx, client, "mkdir -p "+shellQuote(bundleRoot)+" && install -d -m 0700 -o 1000 -g 1000 "+shellQuote(jenkinsRemoteRoot+"/home")); err != nil {
		return nil, errors.New("JENKINS_RUNTIME_PREPARE_FAILED")
	}
	for _, file := range bundle {
		if err := writeRemoteFileBytes(ctx, client, bundleRoot+"/"+filepath.ToSlash(file.RelativePath), file.Content); err != nil {
			return nil, errors.New("JENKINS_RUNTIME_UPLOAD_FAILED")
		}
	}
	input.LogStep("jenkins", "upload-plugins", "info", "上传已校验的 Jenkins 插件及其依赖")
	if err := uploadJenkinsPluginArchive(ctx, client, bundleRoot, archive, digest); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("JENKINS_RUNTIME_UPLOAD_FAILED")
	}
	if err := remoteRun(ctx, client, jenkinsUserNamespaceCommand()); err != nil {
		return nil, errors.New("JENKINS_ROOTLESS_USER_NAMESPACE_UNAVAILABLE")
	}
	for _, target := range []string{"broker", "controller"} {
		image := input.JenkinsImage
		if target == "broker" {
			image += "-broker"
		}
		input.LogStep("jenkins", "build-image", "info", "构建受管运行镜像 "+target)
		command := "docker build --target " + shellQuote(target) + " --label iquops.owner=iquops --label iquops.component=jenkins --label " + shellQuote("iquops.bundle-version="+input.JenkinsBundleVersion) + " -t " + shellQuote(image) + " " + shellQuote(bundleRoot)
		if _, err := remoteOutput(ctx, client, interruptibleRemoteCommand(command)); err != nil {
			return nil, errors.New(stableJenkinsImageBuildError(err))
		}
	}
	input.LogStep("jenkins", "preflight-broker", "info", "从隔离执行器镜像检查 Docker 套接字及构建网络的防火墙链")
	output, err := remoteOutput(ctx, client, jenkinsBrokerPreflightCommand(input.JenkinsImage+"-broker"))
	firewall, err := jenkinsBrokerPreflightResult(output, err)
	if err != nil {
		return nil, err
	}
	config := map[string]any{"adminUser": "iquops-control", "adminPassword": jenkinsRandomSecret(), "brokerToken": jenkinsRandomSecret(), "controllerToken": jenkinsRandomSecret(), "jenkinsPort": input.JenkinsPort, "brokerPort": input.JenkinsPort + 1}
	encoded, _ := json.Marshal(config)
	control := shellQuote(jenkinsRemoteRoot + "/control.json")
	command := "set -eu\numask 077\ntmp=$(mktemp " + control + ".XXXXXXXX)\ntrap 'rm -f \"$tmp\"' EXIT\ncat > \"$tmp\"\nchown 1000:1000 \"$tmp\"\nchmod 0600 \"$tmp\"\nmv -f \"$tmp\" " + control
	if err := writeRemoteStream(ctx, client, command, bytes.NewReader(encoded)); err != nil {
		return nil, errors.New("JENKINS_SECURITY_CONFIG_FAILED")
	}
	input.LogStep("jenkins", "start-broker", "info", "启动隔离执行器，构建防火墙后端为 "+firewall)
	if err := remoteRun(ctx, client, "set -e\n"+removeOwnedJenkinsContainer(containers["jenkins-broker"])+jenkinsBrokerRunCommand(input, firewall)); err != nil {
		return nil, jenkinsContainerStartError(err, "JENKINS_BROKER_START_FAILED")
	}
	input.LogStep("jenkins", "start-controller", "info", "启动 Jenkins 控制器")
	if err := remoteRun(ctx, client, "set -e\n"+removeOwnedJenkinsContainer(containers["jenkins"])+jenkinsDockerRunCommand(input)); err != nil {
		return nil, jenkinsContainerStartError(err, "JENKINS_CONTAINER_START_FAILED")
	}
	health := "for attempt in $(seq 1 90); do " + jenkinsBrokerCommand(input.JenkinsContainer, "health", "") + " >/dev/null 2>&1 && exit 0; sleep 2; done; exit 1"
	if _, err := remoteOutput(ctx, client, health); err != nil {
		return nil, errors.New("JENKINS_AUTHENTICATED_HEALTHCHECK_FAILED")
	}
	return managedJenkinsRuntimeResult(input, true, "running"), nil
}

func jenkinsRandomSecret() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}
func jenkinsDockerRunCommand(input ServerRuntimeExecutionInput) string {
	args := []string{"docker run -d", "--name " + shellQuote(input.JenkinsContainer), "--restart unless-stopped", "--network host", "--security-opt no-new-privileges:true", "--cap-drop ALL", "--env " + shellQuote("JAVA_OPTS=-Djenkins.install.runSetupWizard=false -Dhudson.plugins.git.GitSCM.ALLOW_LOCAL_CHECKOUT=true"), "--env " + shellQuote("JENKINS_OPTS=--httpPort="+strconv.Itoa(input.JenkinsPort)+" --httpListenAddress=127.0.0.1"), "--volume " + shellQuote(jenkinsRemoteRoot+"/home:/var/jenkins_home"), "--volume " + shellQuote(jenkinsRemoteRoot+"/control.json:/run/iquops/control.json:ro"), "--label iquops.owner=iquops", "--label iquops.component=jenkins", "--label " + shellQuote("iquops.bundle-version="+input.JenkinsBundleVersion), shellQuote(input.JenkinsImage)}
	return strings.Join(args, " ")
}
func jenkinsBrokerRunCommand(input ServerRuntimeExecutionInput, firewall string) string {
	args := []string{"docker run -d", "--env " + shellQuote("IQUOPS_IPTABLES_BIN="+firewall), "--name " + shellQuote(input.JenkinsContainer+"-broker"), "--restart unless-stopped", "--network host", "--cap-add NET_ADMIN", jenkinsBrokerSocketMount, "--volume " + shellQuote(jenkinsRemoteRoot+":"+jenkinsRemoteRoot), "--volume " + shellQuote(jenkinsRemoteRoot+"/control.json:/run/iquops/control.json:ro"), "--label iquops.owner=iquops", "--label iquops.component=jenkins-broker", shellQuote(input.JenkinsImage + "-broker")}
	return strings.Join(args, " ")
}
func jenkinsBrokerCommand(container, action, id string) string {
	result := "docker exec -i " + shellQuote(container+"-broker") + " node /opt/iquops/runtime/broker-cli.mjs " + shellQuote(action)
	if id != "" {
		result += " " + shellQuote(id)
	}
	return result
}
func remoteJenkinsReady(ctx context.Context, client sshSessionClient, container string) bool {
	_, err := remoteOutput(ctx, client, jenkinsBrokerCommand(container, "health", ""))
	return err == nil
}
func jenkinsUserNamespaceCommand() string {
	return "set -eu\ncurrent=$(sysctl -n user.max_user_namespaces)\nif [ \"$current\" -lt 15000 ]; then\n test -f " + shellQuote(jenkinsRemoteRoot+"/userns.previous") + " || printf '%s' \"$current\" > " + shellQuote(jenkinsRemoteRoot+"/userns.previous") + "\n printf '%s\\n' '# IQuOps 受管构建器' 'user.max_user_namespaces = 15000' > /etc/sysctl.d/90-iquops-buildkit.conf\n sysctl -w user.max_user_namespaces=15000 >/dev/null\nfi"
}
func jenkinsUserNamespaceRestoreCommand() string {
	return "if [ -f " + shellQuote(jenkinsRemoteRoot+"/userns.previous") + " ] && [ \"$(sysctl -n user.max_user_namespaces)\" = 15000 ] && grep -qx '# IQuOps 受管构建器' /etc/sysctl.d/90-iquops-buildkit.conf; then old=$(cat " + shellQuote(jenkinsRemoteRoot+"/userns.previous") + "); sysctl -w user.max_user_namespaces=\"$old\" >/dev/null; rm -f /etc/sysctl.d/90-iquops-buildkit.conf; fi"
}

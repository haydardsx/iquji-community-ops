package application

import (
	"context"
	"errors"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (e *SystemServerRuntimeExecutor) UninstallJenkins(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	input = withManagedRuntimeDefaults(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	input.LogStep("jenkins", "uninstall", "info", "正在移除受管 Jenkins")
	containers, err := inspectJenkinsContainers(ctx, client, input.JenkinsContainer)
	if err != nil {
		return nil, err
	}
	command := strings.Join([]string{
		"set -e",
		removeOwnedJenkinsContainer(containers["jenkins"]),
		removeOwnedJenkinsContainer(containers["jenkins-broker"]),
		jenkinsUserNamespaceRestoreCommand(),
		"docker image rm -f " + shellQuote(input.JenkinsImage) + " " + shellQuote(input.JenkinsImage+"-broker") + " >/dev/null 2>&1 || true",
		"rm -rf " + shellQuote(jenkinsRemoteRoot),
		"if docker inspect " + shellQuote(input.JenkinsContainer) + " >/dev/null 2>&1; then exit 1; fi",
	}, "\n")
	if _, err := remoteOutput(ctx, client, command); err != nil {
		return nil, errors.New("JENKINS_UNINSTALL_FAILED")
	}
	return managedJenkinsRuntimeResult(input, false, "missing"), nil
}

func loadJenkinsRuntimeBundle(root string) ([]jenkinsBundleFile, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("JENKINS_BUNDLE_NOT_CONFIGURED")
	}
	required := map[string]bool{
		"Dockerfile":                                false,
		"plugins.txt":                               false,
		"plugins.lock":                              false,
		"run-release.mjs":                           false,
		"broker-access.mjs":                         false,
		"broker-cli.mjs":                            false,
		"broker-common.mjs":                         false,
		"broker-firewall.mjs":                       false,
		"broker-registry.mjs":                       false,
		"broker-build-groups.mjs":                   false,
		"broker-variables.mjs":                      false,
		"broker-state.mjs":                          false,
		"broker-source.mjs":                         false,
		"broker-isolation.mjs":                      false,
		"broker-deploy.mjs":                         false,
		"broker-volumes.mjs":                        false,
		"library/vars/iquopsPipeline.groovy":        false,
		"init.groovy.d/01-managed-security.groovy":  false,
		"init.groovy.d/02-seed-release-jobs.groovy": false,
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return nil, errors.New("JENKINS_BUNDLE_READ_FAILED")
	}
	defer directory.Close()
	files := []jenkinsBundleFile{}
	symlinkError := errors.New("JENKINS_BUNDLE_SYMLINK_FORBIDDEN")
	err = fs.WalkDir(directory.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if relative == "." {
				return nil
			}
			for name := range required {
				if strings.HasPrefix(name, relative+"/") {
					return nil
				}
			}
			return fs.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return symlinkError
		}
		if _, declared := required[relative]; !declared {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("JENKINS_BUNDLE_READ_FAILED")
		}
		content, err := directory.ReadFile(relative)
		if err != nil {
			return err
		}
		files = append(files, jenkinsBundleFile{RelativePath: relative, Content: content})
		required[relative] = true
		return nil
	})
	if err != nil {
		if errors.Is(err, symlinkError) {
			return nil, symlinkError
		}
		return nil, errors.New("JENKINS_BUNDLE_READ_FAILED")
	}
	missing := []string{}
	for path, present := range required {
		if !present {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, errors.New("JENKINS_BUNDLE_INCOMPLETE:" + strings.Join(missing, ","))
	}
	sort.Slice(files, func(i, j int) bool { return files[i].RelativePath < files[j].RelativePath })
	return files, nil
}

func swarmRuntimeResult(input ServerRuntimeExecutionInput, status remoteSwarmStatus, ready bool) domain.Object {
	metrics := domain.Object{
		"ssh":                   true,
		"docker":                true,
		"swarm":                 ready,
		"swarmStatus":           status.Status,
		"swarmRole":             status.Role,
		"swarmControlAvailable": status.ControlAvailable,
		"swarmConnectionMode":   swarmAddressConnectionMode(status.AdvertiseAddr),
		"swarmManagerAddress":   status.ManagerAddress,
	}
	if status.NodeID != "" {
		metrics["swarmNodeId"] = status.NodeID
	}
	if status.ClusterID != "" {
		metrics["swarmClusterId"] = status.ClusterID
	}
	if status.AdvertiseAddr != "" {
		metrics["swarmAdvertiseAddr"] = status.AdvertiseAddr
	}
	if ready && input.SwarmMode == "worker" && input.SwarmManagerServerID != "" {
		metrics["swarmManagerServerId"] = input.SwarmManagerServerID
	}
	if ready && input.SwarmMode == "manager" {
		metrics["swarmNetwork"] = firstNonEmptyString(input.TraefikNetwork, "iquops-public")
	}
	checkStatus := serverRuntimeCheckSuccess
	summary := "Docker Swarm removed"
	if ready {
		summary = "Docker Swarm " + status.Role + " is active"
	}
	return domain.Object{
		"metrics": metrics,
		"checks": []domain.Object{
			serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
			serverRuntimeCheck("docker", serverRuntimeCheckSuccess, "Docker available"),
			serverRuntimeCheck("swarm", checkStatus, summary),
		},
	}
}

func managedJenkinsRuntimeResult(input ServerRuntimeExecutionInput, ready bool, status string) domain.Object {
	metrics := domain.Object{
		"ssh":                  true,
		"docker":               true,
		"jenkins":              ready,
		"jenkinsStatus":        status,
		"jenkinsImage":         input.JenkinsImage,
		"jenkinsContainer":     input.JenkinsContainer,
		"jenkinsBundleVersion": input.JenkinsBundleVersion,
		"jenkinsPort":          input.JenkinsPort,
	}
	summary := "Managed Jenkins removed"
	if ready {
		metrics["dockerRegistryMirrorReady"] = true
		summary = "Managed Jenkins is ready"
	}
	return domain.Object{
		"metrics": metrics,
		"checks": []domain.Object{
			serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
			serverRuntimeCheck("docker", serverRuntimeCheckSuccess, "Docker available"),
			serverRuntimeCheck("jenkins", serverRuntimeCheckSuccess, summary),
		},
	}
}

func stableJenkinsImageBuildError(err error) string {
	switch {
	case err == nil:
		return "JENKINS_IMAGE_BUILD_FAILED"
	case errors.Is(err, context.Canceled):
		return "OPERATION_CANCELLED"
	case errors.Is(err, context.DeadlineExceeded):
		return "OPERATION_TIMEOUT"
	}
	// 构建输出也会列出成功或命中缓存的步骤，只用失败行判断出错的命令。
	for line := range strings.Lines(strings.ToLower(err.Error())) {
		if strings.Contains(line, "failed to resolve source metadata") ||
			strings.Contains(line, "failed to fetch anonymous token") ||
			strings.Contains(line, "failed to fetch oauth token") ||
			strings.Contains(line, "pull access denied") ||
			(strings.Contains(line, "registry-1.docker.io") && strings.Contains(line, "failed")) {
			return "JENKINS_BASE_IMAGE_PULL_FAILED"
		}
		if strings.Contains(line, "exit code:") || strings.Contains(line, "exit status") || strings.Contains(line, "returned a non-zero code") {
			switch {
			case strings.Contains(line, "jenkins-plugin-cli"):
				return "JENKINS_PLUGIN_INSTALL_FAILED"
			case strings.Contains(line, "apt-get"):
				return "JENKINS_SYSTEM_PACKAGE_INSTALL_FAILED"
			}
		}
	}
	return "JENKINS_IMAGE_BUILD_FAILED"
}

func stableJenkinsHealthError(logs string, rootReady bool) string {
	message := strings.ToLower(logs)
	if strings.Contains(message, "permission denied") || strings.Contains(message, "cannot open") {
		return "JENKINS_RUNTIME_PERMISSION_FAILED"
	}
	if strings.Contains(message, "failed loading plugin") || strings.Contains(message, "failed to load plugin") || strings.Contains(message, "failed to load:") {
		return "JENKINS_PLUGIN_INITIALIZATION_FAILED"
	}
	if rootReady {
		return "JENKINS_MANAGED_JOB_MISSING"
	}
	return "JENKINS_HEALTHCHECK_FAILED"
}

func interruptibleRemoteCommand(command string) string {
	return strings.Join([]string{
		"child_pid=''",
		"cleanup() { if [ -n \"$child_pid\" ]; then kill -TERM \"$child_pid\" >/dev/null 2>&1 || true; fi; }",
		"trap cleanup HUP INT TERM",
		"(" + command + ") &",
		"child_pid=$!",
		"wait \"$child_pid\"",
		"status=$?",
		"trap - HUP INT TERM",
		"exit \"$status\"",
	}, "\n")
}

func withManagedRuntimeDefaults(input ServerRuntimeExecutionInput) ServerRuntimeExecutionInput {
	input = withServerRuntimeLogStep(input)
	if input.SwarmMode == "" {
		input.SwarmMode = "manager"
	}
	if input.TraefikNetwork == "" {
		input.TraefikNetwork = "iquops-public"
	}
	if input.JenkinsBundleVersion == "" {
		input.JenkinsBundleVersion = platform.ManagedJenkinsBundleVersion
	}
	if input.JenkinsImage == "" {
		input.JenkinsImage = platform.ManagedJenkinsImage
	}
	if input.JenkinsContainer == "" {
		input.JenkinsContainer = "iquops-jenkins"
	}
	if input.JenkinsPort <= 0 {
		input.JenkinsPort = 58080
	}
	return input
}

func nonEmptyLines(value string) []string {
	lines := []string{}
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

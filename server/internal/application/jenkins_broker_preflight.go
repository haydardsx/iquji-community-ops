package application

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

const jenkinsBrokerSocketMount = "--mount type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock"

func jenkinsBrokerPreflightCommand(image string) string {
	return "docker run --rm --network host --cap-add NET_ADMIN " + jenkinsBrokerSocketMount + " --entrypoint node " + shellQuote(image) + " /opt/iquops/runtime/broker-firewall.mjs"
}

func jenkinsBrokerPreflightResult(output string, err error) (string, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if err == nil {
		switch lastLine {
		case "iptables-legacy", "iptables-nft":
			return lastLine, nil
		}
		return "", errors.New("JENKINS_BROKER_FIREWALL_UNAVAILABLE")
	}
	switch lastLine {
	case "JENKINS_BROKER_FIREWALL_UNAVAILABLE", "JENKINS_BROKER_DOCKER_SOCKET_UNAVAILABLE":
		return "", errors.New(lastLine)
	}
	return "", jenkinsContainerStartError(err, "JENKINS_BROKER_START_FAILED")
}

func jenkinsContainerStartError(err error, fallback string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	detail := strings.ToLower(err.Error())
	switch {
	case strings.Contains(detail, "container name") && strings.Contains(detail, "already in use"):
		return errors.New("JENKINS_CONTAINER_NAME_CONFLICT")
	case strings.Contains(detail, "bind source path does not exist: /var/run/docker.sock"):
		return errors.New("JENKINS_BROKER_DOCKER_SOCKET_UNAVAILABLE")
	case fallback == "JENKINS_BROKER_START_FAILED" && (strings.Contains(detail, "operation not permitted") || strings.Contains(detail, "authorization denied by plugin")):
		return errors.New("JENKINS_BROKER_HOST_ACCESS_DENIED")
	default:
		return errors.New(fallback)
	}
}

func inspectJenkinsContainers(ctx context.Context, client sshSessionClient, controller string) (map[string]string, error) {
	format := "{{.Names}}|{{.ID}}|{{.Label \"iquops.owner\"}}|{{.Label \"iquops.component\"}}"
	name := regexp.QuoteMeta(controller)
	command := "docker container ls -a --no-trunc --filter " + shellQuote("name=^/"+name+"$") +
		" --filter " + shellQuote("name=^/"+name+"-broker$") + " --format " + shellQuote(format)
	output, err := remoteOutput(ctx, client, command)
	if err != nil {
		return nil, jenkinsContainerStartError(err, "JENKINS_DOCKER_REQUIRED")
	}
	return ownedJenkinsContainers(output, controller)
}

func ownedJenkinsContainers(output, controller string) (map[string]string, error) {
	components := map[string]string{controller: "jenkins", controller + "-broker": "jenkins-broker"}
	ids := map[string]string{}
	for line := range strings.SplitSeq(output, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "|")
		component := components[fields[0]]
		if len(fields) != 4 || component == "" || !containerIdentity.MatchString(fields[1]) || fields[2] != "iquops" || fields[3] != component {
			return nil, errors.New("JENKINS_CONTAINER_NAME_CONFLICT")
		}
		ids[component] = fields[1]
	}
	return ids, nil
}

// 只移除已核实归属的完整容器 ID，防止并发替换后误删后来占用同名的容器。
func removeOwnedJenkinsContainer(id string) string {
	if id == "" {
		return ""
	}
	return "docker rm -f " + shellQuote(id) + " >/dev/null\n"
}

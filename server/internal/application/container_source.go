package application

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

var imageReference = regexp.MustCompile(`^([a-z0-9][a-z0-9.-]*(:[0-9]+)?/)?[a-z0-9][a-z0-9._/-]*(:[A-Za-z0-9_][A-Za-z0-9._-]{0,127}|@sha256:[0-9a-f]{64})$`)
var packageIdentifier = regexp.MustCompile(`^([@][a-z0-9][a-z0-9._-]*/)?[a-zA-Z0-9][a-zA-Z0-9._+-]*$`)
var exactPackageVersion = regexp.MustCompile(`^[0-9][a-zA-Z0-9.:_+~%-]*$`)
var portName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

func validImageReference(value string) bool {
	return imageReference.MatchString(value) && !strings.Contains(value, "//") && !strings.HasSuffix(value, ":latest")
}

func stringArguments(value any) ([]string, bool) {
	var args []string
	b, err := json.Marshal(value)
	if err != nil || json.Unmarshal(b, &args) != nil || len(args) == 0 || len(args) > 128 {
		return nil, false
	}
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) || len(arg) > 16384 {
			return nil, false
		}
	}
	return args, args[0] != ""
}

func validateContainerSource(source domain.Object) (domain.Object, error) {
	provider := stringValue(source["provider"])
	if provider == "image" {
		if !objectOnlyContainsKeys(source, "provider", "image", "registryMirrorUrl") || !validImageReference(stringValue(source["image"])) {
			return nil, errors.New("APPLICATION_IMAGE_REFERENCE_INVALID")
		}
		return cloneObject(source), nil
	}
	switch provider {
	case "apt", "dnf", "yum", "npm", "pypi":
	default:
		return nil, errors.New("APPLICATION_SOURCE_PROVIDER_INVALID")
	}
	if !objectOnlyContainsKeys(source, "provider", "packageName", "version", "baseImage", "command", "shellEntrypoint") || !validImageReference(stringValue(source["baseImage"])) {
		return nil, errors.New("APPLICATION_PACKAGE_BASE_IMAGE_INVALID")
	}
	name, version := stringValue(source["packageName"]), stringValue(source["version"])
	if !packageIdentifier.MatchString(name) || (provider != "npm" && strings.HasPrefix(name, "@")) || !exactPackageVersion.MatchString(version) {
		return nil, errors.New("APPLICATION_PACKAGE_VERSION_INVALID")
	}
	if _, ok := stringArguments(source["command"]); !ok || !validShellEntrypoint(source) {
		return nil, errors.New("APPLICATION_PACKAGE_COMMAND_REQUIRED")
	}
	return cloneObject(source), nil
}

func validateContainerRuntime(config domain.Object) error {
	if !validShellEntrypoint(config) {
		return errors.New("APPLICATION_CONTAINER_COMMAND_INVALID")
	}
	names, targets, published := map[string]bool{}, map[string]bool{}, map[string]bool{}
	if value, ok := config["ports"]; ok && !applicationObjectArray(value) {
		return errors.New("APPLICATION_PORT_INVALID")
	}
	for _, p := range objectSlice(config["ports"]) {
		name, protocol := stringValue(p["name"]), stringValue(p["protocol"])
		if !objectOnlyContainsKeys(p, "name", "targetPort", "publishedPort", "protocol") || !portName.MatchString(name) || names[name] || !integerInRange(p["targetPort"], 1, 65535) || (protocol != "http" && protocol != "tcp" && protocol != "udp") {
			return errors.New("APPLICATION_PORT_INVALID")
		}
		transport := protocol
		if transport == "http" {
			transport = "tcp"
		}
		target := strconv.Itoa(int(numberValue(p["targetPort"]))) + "/" + transport
		if targets[target] {
			return errors.New("APPLICATION_PORT_DUPLICATE")
		}
		if v, ok := p["publishedPort"]; ok {
			if !integerInRange(v, 0, 65535) {
				return errors.New("APPLICATION_PUBLISHED_PORT_INVALID")
			}
			if n := int(numberValue(v)); n > 0 {
				key := strconv.Itoa(n) + "/" + transport
				if published[key] {
					return errors.New("APPLICATION_PORT_DUPLICATE")
				}
				published[key] = true
			}
		}
		names[name], targets[target] = true, true
	}
	if err := validateVolumeDeclarations(config); err != nil {
		return err
	}
	for _, key := range []string{"command", "healthCommand", "initializeCommand", "logCommand"} {
		if raw, ok := config[key]; ok {
			if _, valid := stringArguments(raw); !valid {
				return errors.New("APPLICATION_CONTAINER_COMMAND_INVALID")
			}
		}
	}
	return nil
}

// 旧版本的 command 保持 Docker CMD 语义。仅新建的复合启动脚本可以替换镜像入口。
func validShellEntrypoint(config domain.Object) bool {
	mode, set := config["shellEntrypoint"]
	if !set {
		return true
	}
	command, valid := stringArguments(config["command"])
	return mode == true && valid && len(command) == 3 && command[0] == "/bin/sh" && command[1] == "-c"
}

func routeHTTPPort(swarm domain.Object, requested string) (string, error) {
	if explicitNoServicePorts(swarm) {
		return "", errors.New("ROUTE_HTTP_PORT_REQUIRED")
	}
	ports := objectSlice(swarm["ports"])
	if len(ports) == 0 {
		if requested == "" || requested == "web" {
			return requested, nil
		}
		return "", errors.New("ROUTE_HTTP_PORT_REQUIRED")
	}
	var options []string
	for _, p := range ports {
		if p["protocol"] != "http" {
			continue
		}
		name := stringValue(p["name"])
		if requested != "" && name == requested {
			return name, nil
		}
		options = append(options, name)
	}
	if requested == "" && len(options) == 1 {
		return options[0], nil
	}
	return "", errors.New("ROUTE_HTTP_PORT_REQUIRED")
}

package application

import (
	"errors"
	"net/url"
	"path"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func validateApplicationSourceConfig(input domain.Object) (domain.Object, error) {
	input = cloneObject(input)
	if raw, exists := input["registryMirrorUrl"]; exists {
		mirror, err := applicationRegistryMirror(raw)
		if err != nil {
			return nil, err
		}
		if mirror == "" {
			delete(input, "registryMirrorUrl")
		} else {
			input["registryMirrorUrl"] = mirror
		}
	}
	if provider := stringValue(input["provider"]); provider != "git" && provider != "" {
		return validateContainerSource(input)
	}
	if !objectOnlyContainsKeys(input, "provider", "repositoryUrl", "ref", "refType", "jenkinsfilePath", "sourcePath", "authSecretRef", "registryMirrorUrl") {
		return nil, errors.New("APPLICATION_SOURCE_CONFIG_INVALID")
	}
	if strings.TrimSpace(domain.StringValue(input["provider"])) != "git" {
		return nil, errors.New("APPLICATION_SOURCE_PROVIDER_INVALID")
	}
	repositoryURL := strings.TrimSpace(domain.StringValue(input["repositoryUrl"]))
	parsed, err := url.Parse(repositoryURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "ssh") {
		return nil, errors.New("APPLICATION_GIT_URL_INVALID")
	}
	if strings.TrimSpace(parsed.Path) == "" || parsed.Path == "/" {
		return nil, errors.New("APPLICATION_GIT_URL_INVALID")
	}
	ref := strings.TrimSpace(domain.StringValue(input["ref"]))
	if ref == "" {
		ref = "main"
	}
	refType := strings.TrimSpace(domain.StringValue(input["refType"]))
	if refType == "" {
		refType = "branch"
	}
	if refType != "branch" && refType != "tag" && refType != "commit" {
		return nil, errors.New("APPLICATION_GIT_REF_TYPE_INVALID")
	}
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\r\n") {
		return nil, errors.New("APPLICATION_GIT_REF_INVALID")
	}
	jenkinsfilePath := strings.TrimSpace(domain.StringValue(input["jenkinsfilePath"]))
	if jenkinsfilePath == "" {
		jenkinsfilePath = "Jenkinsfile"
	}
	if !isSafeRepositoryPath(jenkinsfilePath) || path.Clean(jenkinsfilePath) == "." {
		return nil, errors.New("APPLICATION_JENKINSFILE_PATH_INVALID")
	}
	sourcePath := strings.TrimSpace(domain.StringValue(input["sourcePath"]))
	if sourcePath == "" {
		sourcePath = "."
	}
	if !isSafeRepositoryPath(sourcePath) && sourcePath != "." {
		return nil, errors.New("APPLICATION_SOURCE_PATH_INVALID")
	}
	normalized := domain.CloneObjectWithoutUserReferences(input)
	normalized["provider"] = "git"
	normalized["repositoryUrl"] = repositoryURL
	normalized["ref"] = ref
	normalized["refType"] = refType
	normalized["jenkinsfilePath"] = jenkinsfilePath
	normalized["sourcePath"] = sourcePath
	if authSecretRef := strings.TrimSpace(domain.StringValue(input["authSecretRef"])); authSecretRef != "" {
		normalized["authSecretRef"] = authSecretRef
	} else {
		delete(normalized, "authSecretRef")
	}
	return normalized, nil
}

func isSafeRepositoryPath(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\\x00\r\n") || path.IsAbs(value) {
		return false
	}
	clean := path.Clean(value)
	return clean != ".." && !strings.HasPrefix(clean, "../")
}

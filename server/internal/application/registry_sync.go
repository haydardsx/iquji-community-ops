package application

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 仓库名必须明确指定注册表。标签由每次发布的构建组和平台唯一确定。
var registryRepositoryPattern = regexp.MustCompile(`^([a-z0-9]+(?:[.-][a-z0-9]+)*)(?::([0-9]{1,5}))?/(?:[a-z0-9]+(?:[._-]+[a-z0-9]+)*/)*[a-z0-9]+(?:[._-]+[a-z0-9]+)*$`)

func registrySyncConfig(config domain.Object) domain.Object {
	raw, exists := objectMapFromAny(config["deliveryConfig"])["registrySync"]
	if !exists {
		return nil
	}
	return objectMapFromAny(raw)
}

func validateRegistrySync(sync domain.Object) error {
	invalid := errors.New("APPLICATION_REGISTRY_SYNC_INVALID")
	if !objectOnlyContainsKeys(sync, "enabled", "imageRepository", "registryUsername", "registrySecretRef") {
		return invalid
	}
	enabled, ok := sync["enabled"].(bool)
	if !ok {
		return invalid
	}
	if !enabled {
		return nil
	}
	for _, key := range []string{"imageRepository", "registryUsername", "registrySecretRef"} {
		if value, present := sync[key]; present {
			if _, ok := value.(string); !ok {
				return invalid
			}
		}
	}
	repository, username, ref := stringValue(sync["imageRepository"]), stringValue(sync["registryUsername"]), stringValue(sync["registrySecretRef"])
	match := registryRepositoryPattern.FindStringSubmatch(repository)
	if len(match) == 0 || len(repository) > 255 || (!strings.Contains(match[1], ".") && match[2] == "" && match[1] != "localhost") {
		return invalid
	}
	if match[2] != "" {
		port, _ := strconv.Atoi(match[2])
		if port < 1 || port > 65535 {
			return invalid
		}
	}
	if (username == "") != (ref == "") || strings.ContainsAny(username, ":\x00\r\n") || len(username) > 256 {
		return invalid
	}
	return nil
}

func validateRegistryCredential(state domain.State, sync domain.Object) error {
	if ref := stringValue(sync["registrySecretRef"]); sync["enabled"] == true && ref != "" {
		asset := credentialForPurpose(state, ref, "registry_secret")
		if asset == nil || stringValue(asset["materialRef"]) == "" {
			return errors.New("APPLICATION_CREDENTIAL_INVALID")
		}
	}
	return nil
}

func (r *Runtime) registryPushMaterials(ctx context.Context, state domain.State, plan domain.Object) (domain.Object, error) {
	sync := registrySyncConfig(objectMapFromAny(plan["buildSnapshot"]))
	if sync == nil {
		return nil, nil
	}
	if err := validateRegistrySync(sync); err != nil {
		return nil, err
	}
	if sync["enabled"] != true {
		return nil, nil
	}
	result := domain.Object{"repository": sync["imageRepository"]}
	if ref := stringValue(sync["registrySecretRef"]); ref != "" {
		revision := stringValue(plan["registryRevisionSecretRef"])
		if revision == "" || revision == ref {
			return nil, errors.New("APPLICATION_CREDENTIAL_INVALID")
		}
		password, err := r.readPurposeMaterial(ctx, state, domain.Object{"secretRef": ref, "materialRevisionRef": revision}, "registry_secret")
		if err != nil {
			return nil, err
		}
		result["username"], result["password"] = sync["registryUsername"], password
	}
	return result, nil
}

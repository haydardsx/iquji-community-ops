package application

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

var registryMirrorPath = regexp.MustCompile(`^(/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
var registryMirrorAuthority = regexp.MustCompile(`^(?:[a-z0-9]+(?:[.-][a-z0-9-]+)*|\[[0-9a-f:]+\])(?::[0-9]+)?$`)

// 应用覆盖只用于 Docker Hub 拉取。镜像坐标和 BuildKit 配置共用同一校验。
func applicationRegistryMirror(value any) (string, error) {
	address, ok := domain.DockerRegistryMirrorURL(value)
	if !ok {
		return "", errors.New("DOCKER_REGISTRY_MIRROR_URL_INVALID")
	}
	if address == "" {
		return "", nil
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.RawPath != "" || !registryMirrorPath.MatchString(u.Path) || !registryMirrorAuthority.MatchString(strings.ToLower(u.Host)) {
		return "", errors.New("DOCKER_REGISTRY_MIRROR_URL_INVALID")
	}
	u.Host = strings.ToLower(u.Host)
	if !strings.ContainsAny(u.Host, ".:") && u.Host != "localhost" {
		u.Host += ":443"
	}
	return u.String(), nil
}

func imagePullReference(image string, override any) (string, error) {
	mirror, err := applicationRegistryMirror(override)
	if mirror == "" || err != nil {
		return image, err
	}
	repository := registryRepository(image)
	hubPath, dockerHub := strings.CutPrefix(repository, "docker.io/")
	if !dockerHub {
		return image, nil
	}
	suffix := strings.LastIndex(image, "@")
	if suffix < 0 {
		suffix = strings.LastIndex(image, ":")
	}
	if suffix <= strings.LastIndex(image, "/") {
		return "", errors.New("APPLICATION_IMAGE_REFERENCE_INVALID")
	}
	return strings.TrimPrefix(mirror, "https://") + "/" + hubPath + image[suffix:], nil
}

// 拉取配置按应用、所在服务器、该节点最近探测到的 Docker daemon 配置依次解析。
func resolvePullMirrors(source, server domain.Object) (domain.Object, error) {
	desired, err := desiredDockerRegistryMirrorURL(server)
	if err != nil {
		return nil, err
	}
	observed := []string{}
	for _, raw := range objectStringSlice(serverRuntimeMetrics(server)["dockerRegistryMirrors"]) {
		address, ok := domain.DockerRegistryMirrorURL(raw)
		if !ok || address == "" {
			return nil, errors.New("DOCKER_REGISTRY_MIRROR_URL_INVALID")
		}
		observed = append(observed, address)
	}
	trusted := observed
	if desired != "" {
		trusted = []string{desired}
	}
	override, err := applicationRegistryMirror(source["registryMirrorUrl"])
	if err != nil {
		return nil, err
	}
	if override != "" {
		return domain.Object{"origin": "application", "mirrors": []string{override}, "trustedMirrors": trusted, "pullMirror": override}, nil
	}
	if desired == "" {
		_, probed := serverRuntimeMetrics(server)["dockerRegistryMirrors"]
		return domain.Object{"origin": "daemon", "mirrors": observed, "trustedMirrors": trusted, "pullMirror": "", "verifyDaemon": probed}, nil
	}
	pin := ""
	if strings.HasPrefix(desired, "https://") {
		pin, err = applicationRegistryMirror(desired)
		if err != nil {
			return nil, err
		}
	}
	return domain.Object{"origin": "server", "mirrors": []string{desired}, "trustedMirrors": trusted, "pullMirror": pin, "verifyDaemon": pin == ""}, nil
}

func snapshotNodePullMirrors(state domain.State, config domain.Object) (domain.Object, error) {
	source := objectMapFromAny(config["sourceConfig"])
	result := domain.Object{}
	var nodes []string
	if source["provider"] == "image" {
		if !strings.HasPrefix(registryRepository(stringValue(source["image"])), "docker.io/") {
			return result, nil
		}
		for _, svc := range objectSlice(objectMapFromAny(config["swarmRuntime"])["services"]) {
			nodes = append(nodes, stringValue(svc["serverId"]))
		}
	} else {
		nodes = []string{stringValue(config["buildServerId"]), stringValue(config["deploymentManagerServerId"])}
	}
	for _, id := range nodes {
		if _, seen := result[id]; seen {
			continue
		}
		frozen, err := resolvePullMirrors(source, findObject(state.Servers, id))
		if err != nil {
			return nil, err
		}
		result[id] = frozen
	}
	return result, nil
}

// 构建历史只返回镜像源的选择依据和经过校验的地址，不返回节点连接或认证材料。
func publicPullMirrorFacts(plan, snapshot domain.Object) []domain.Object {
	frozen := objectMapFromAny(snapshot["nodePullMirrors"])
	ids := make([]string, 0, len(frozen))
	for id := range frozen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]domain.Object, 0, len(ids))
	for _, id := range ids {
		item := objectMapFromAny(frozen[id])
		role := "deploy"
		if plan["buildServerId"] != nil {
			switch {
			case plan["buildServerId"] == id && plan["deploymentManagerServerId"] == id:
				role = "buildGateway"
			case plan["buildServerId"] == id:
				role = "build"
			default:
				role = "gateway"
			}
		}
		addresses := []string{}
		for _, address := range objectStringSlice(item["mirrors"]) {
			if parsed, ok := domain.DockerRegistryMirrorURL(address); ok && parsed != "" {
				addresses = append(addresses, parsed)
			}
		}
		result = append(result, domain.Object{"serverId": id, "role": role, "origin": item["origin"], "addresses": addresses, "probed": item["origin"] != "daemon" || item["verifyDaemon"] == true})
	}
	return result
}

// 旧节点第一次读取 daemon 成功后固定该结果，任务重试继续核验同一份镜像列表。
func (r *Runtime) pinFirstDaemonMirrors(ctx context.Context, planID, serverID string, observed []string) error {
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		plan := findObject(state.ReleasePlans, planID)
		if plan == nil {
			return errors.New("RELEASE_PLAN_NOT_FOUND")
		}
		node := objectMapFromAny(objectMapFromAny(objectMapFromAny(plan["buildSnapshot"])["nodePullMirrors"])[serverID])
		if node["origin"] != "daemon" {
			return nil
		}
		if node["verifyDaemon"] == true {
			if !slices.Equal(observed, objectStringSlice(node["mirrors"])) {
				return errors.New("DOCKER_REGISTRY_MIRROR_DRIFT")
			}
			return nil
		}
		node["mirrors"], node["trustedMirrors"], node["verifyDaemon"] = observed, observed, true
		return nil
	})
}

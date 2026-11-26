package application

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type JenkinsAccessInput struct {
	DomainID        string `json:"domainId"`
	GatewayServerID string `json:"gatewayServerId"`
	ViewerPassword  string `json:"viewerPassword"`
}

func jenkinsDomainInUse(state domain.State, domainID, exceptServer string) bool {
	for _, server := range state.Servers {
		if server["id"] == exceptServer {
			continue
		}
		for _, snap := range domain.JenkinsAccessSnapshots(server) {
			if snap["domainId"] == domainID {
				return true
			}
		}
	}
	return false
}

func (r *Runtime) JenkinsAccess(ctx context.Context, id string) (domain.Object, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	server := findObject(state.Servers, id)
	if server == nil {
		return nil, errors.New("SERVER_NOT_FOUND")
	}
	health := objectMapFromAny(server["health"])
	metrics := serverRuntimeMetrics(server)
	access := objectMapFromAny(server["jenkinsAccess"])
	result := domain.Object{"status": firstNonEmptyString(stringValue(access["status"]), "unbound"), "viewerUsername": "iquops-viewer", "ready": metrics["jenkins"] == true, "version": stringValue(metrics["jenkinsVersion"]), "checkedAt": stringValue(health["checkedAt"])}
	for _, key := range []string{"domainId", "gatewayServerId", "url"} {
		result[key] = stringValue(objectMapFromAny(access["applied"])[key])
	}
	return result, nil
}

func (r *Runtime) CreateJenkinsAccess(ctx context.Context, id string, input JenkinsAccessInput, remove bool) (domain.Task, error) {
	action := domain.TaskActionBindJenkinsAccess
	if remove {
		action = domain.TaskActionUnbindJenkinsAccess
	}
	task := newTask(string(action), "server", id, "Jenkins", nil)
	materials := map[string]string{}
	err := r.Store.MutateStateWithSecretMaterials(ctx, materials, func(state *domain.State) error {
		if reused, err := reuseTaskRequest(ctx, *state, &task); reused || err != nil {
			return err
		}
		server := findObject(state.Servers, id)
		if server == nil {
			return errors.New("SERVER_NOT_FOUND")
		}
		if old := objectMapFromAny(objectMapFromAny(server["jenkinsAccess"])["applied"]); len(old) > 0 {
			task.Payload["previousBinding"] = domain.Object{"domainId": old["domainId"], "gatewayServerId": old["gatewayServerId"]}
		}
		if !remove {
			if len([]rune(input.ViewerPassword)) < 12 || len(input.ViewerPassword) > 128 || strings.IndexFunc(input.ViewerPassword, unicode.IsControl) >= 0 {
				return errors.New("JENKINS_ACCESS_PASSWORD_INVALID")
			}
			snapshot, err := r.jenkinsAccessBinding(*state, id, input.DomainID, input.GatewayServerID)
			if err != nil {
				return err
			}
			task.Payload["domainId"], task.Payload["gatewayServerId"] = snapshot["domainId"], snapshot["gatewayServerId"]
			ref := domain.NewID("pending_secret")
			task.Payload["stagedSecretRef"] = ref
			materials[ref] = input.ViewerPassword
		}
		return enqueueTaskRequest(ctx, state, &task)
	})
	return task, err
}

func (r *Runtime) jenkinsAccessBinding(state domain.State, id, domainID, gatewayID string) (domain.Object, error) {
	server := findObject(state.Servers, id)
	if serverRuntimeMetrics(server)["jenkins"] != true || jenkinsRuntimeBundleRevision(server) < 13 {
		return nil, errors.New("JENKINS_ACCESS_UPGRADE_REQUIRED")
	}
	gateway := findObject(state.Servers, gatewayID)
	if gateway == nil || serverRuntimeMetrics(gateway)["traefik"] != true {
		return nil, errors.New("DOMAIN_REDIRECT_GATEWAY_REQUIRED")
	}
	asset := findObject(state.Domains, domainID)
	bound, err := routeDomainSnapshot(state, asset, gatewayID)
	if err != nil {
		return nil, err
	}
	if bound["tls"] != true {
		return nil, errors.New("JENKINS_ACCESS_CERTIFICATE_REQUIRED")
	}
	if redirectBlocksBackend(asset, "https") || jenkinsDomainInUse(state, domainID, id) {
		return nil, errors.New("DOMAIN_REDIRECT_HOST_IN_USE")
	}
	if err := validateRedirectHostAvailable(state, asset, domain.Object{"targetDomainId": ""}); err != nil {
		return nil, err
	}
	host := stringValue(asset["name"])
	port := numberOrDefault(serverRuntimeMetrics(gateway)["traefikHttpsPort"], r.Config.TraefikHTTPSPort)
	publicHost := host
	if port != 443 {
		publicHost = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return domain.Object{"domainId": domainID, "gatewayServerId": gatewayID, "host": host, "certificateId": asset["certificateId"], "url": (&url.URL{Scheme: "https", Host: publicHost, Path: "/"}).String(), "httpsPort": port}, nil
}

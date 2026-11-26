package application

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 旧版本曾将 HTTPS 升级写入应用路由文件。仅处理仍有待核验或已发布快照的文件。
func (r *Runtime) retireLegacyRedirects(ctx context.Context, state domain.State, source domain.Object, task domain.Task) error {
	for _, route := range state.Routes {
		byGateway := map[string][]domain.Object{}
		for _, snapshot := range possibleRouteSnapshots(route) {
			if bound := findObject(objectSlice(snapshot["domains"]), task.TargetID); bound["httpsRedirectEnabled"] == true || bound["inheritedUpgrade"] == true {
				id := stringValue(snapshot["gatewayServerId"])
				byGateway[id] = append(byGateway[id], snapshot)
			}
		}
		for gatewayID, snapshots := range byGateway {
			before := snapshots[len(snapshots)-1]
			input, err := r.redirectRuntimeInput(ctx, state, source, nil, before, task)
			if err != nil {
				return err
			}
			bound := findObject(objectSlice(before["domains"]), task.TargetID)
			input.Route, input.Domains, input.Rules = route, []domain.Object{bound}, objectSlice(before["rules"])
			input.Redirect, input.LegacyRedirectHost = nil, stringValue(bound["name"])
			result, err := r.RouteExecutor.Apply(ctx, input)
			if err != nil {
				return err
			}
			err = r.Store.MutateState(ctx, func(current *domain.State) error {
				item := findObject(current.Routes, stringValue(route["id"]))
				for _, snapshot := range possibleRouteSnapshots(item) {
					if snapshot["gatewayServerId"] != gatewayID {
						continue
					}
					bound := findObject(objectSlice(snapshot["domains"]), task.TargetID)
					delete(bound, "httpsRedirectEnabled")
					delete(bound, "inheritedUpgrade")
					if snapshot["revision"] == result["previousRevision"] {
						snapshot["revision"] = result["revision"]
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// 保留旧文件中实际发布的服务和路径，避免迁移时误发应用的当前草稿。
func retireRemoteLegacyRedirect(ctx context.Context, client *ssh.Client, input RouteRuntimeInput) (domain.Object, error) {
	if err := inspectRouteGateway(ctx, client, input); err != nil {
		return nil, err
	}
	path, staged, backup := routeRemotePaths(stringValue(input.Route["id"]))
	raw, err := remoteOutput(ctx, client, "if test -f "+shellQuote(path)+"; then cat "+shellQuote(path)+"; fi")
	if err != nil {
		return nil, errors.New("ROUTE_CONFIG_READ_FAILED")
	}
	if raw == "" {
		_, err = verifyRouteAccess(ctx, client, input, true)
		return nil, err
	}
	revised, old, revision, err := retireLegacyDocument([]byte(raw), stringValue(input.Route["id"]), input.LegacyRedirectHost)
	if err != nil {
		return nil, err
	}
	if len(revised) > 0 {
		if err := writeRemoteFileBytes(ctx, client, staged, revised); err != nil {
			return nil, errors.New("ROUTE_CONFIG_UPLOAD_FAILED")
		}
		command := "set -eu; mkdir -p " + shellQuote(traefikRemoteRoot+"/route-backups") + "; cp " + shellQuote(path) + " " + shellQuote(backup) + "; mv " + shellQuote(staged) + " " + shellQuote(path)
		if err := remoteRun(ctx, client, command); err != nil {
			return nil, errors.New("ROUTE_CONFIG_REPLACE_FAILED")
		}
	}
	input.Revision = revision
	if _, err = verifyRouteAccess(ctx, client, input, false); err != nil {
		if len(revised) > 0 {
			recoverCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if remoteRun(recoverCtx, client, "mv "+shellQuote(backup)+" "+shellQuote(path)) != nil {
				return nil, errors.New("ROUTE_ROLLBACK_FAILED")
			}
		}
		return nil, err
	}
	_ = remoteRun(ctx, client, "rm -f "+shellQuote(backup))
	return domain.Object{"previousRevision": old, "revision": revision}, nil
}

func retireLegacyDocument(data []byte, routeID, host string) ([]byte, string, string, error) {
	var document domain.Object
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, "", "", errors.New("ROUTE_CONFIG_INVALID")
	}
	httpConfig := objectMapFromAny(document["http"])
	middlewares := objectMapFromAny(httpConfig["middlewares"])
	prefix := routeRevision(routeID)
	marker := "iquops-" + prefix + "-revision"
	headers := objectMapFromAny(objectMapFromAny(objectMapFromAny(middlewares[marker])["headers"])["customResponseHeaders"])
	old, ok := strings.CutPrefix(stringValue(headers["X-IQuOps-Route"]), prefix+":")
	if !ok || old == "" {
		return nil, "", "", errors.New("ROUTE_CONFIG_OWNERSHIP_MISMATCH")
	}
	changed := false
	for name, value := range objectMapFromAny(httpConfig["routers"]) {
		router := objectMapFromAny(value)
		base, ok := strings.CutSuffix(name, "-http")
		if !ok || !strings.HasPrefix(stringValue(router["rule"]), "Host("+strconv.Quote(host)+") && ") {
			continue
		}
		legacy := base + "-https"
		oldMiddleware := objectMapFromAny(middlewares[legacy])
		if (oldMiddleware["redirectScheme"] == nil && oldMiddleware["redirectRegex"] == nil) || !containsString(objectStringSlice(router["middlewares"]), legacy) {
			continue
		}
		chain := []string{marker}
		if _, ok := middlewares[base+"-path"]; ok {
			chain = append(chain, base+"-path")
		}
		router["middlewares"] = chain
		delete(middlewares, legacy)
		changed = true
	}
	if !changed {
		return nil, old, old, nil
	}
	revision := routeRevision([]string{old, host, "retire-https-upgrade"})
	headers["X-IQuOps-Route"] = prefix + ":" + revision
	revised, err := json.Marshal(document)
	return revised, old, revision, err
}

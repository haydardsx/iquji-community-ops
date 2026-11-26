package application

import (
	"errors"
	"regexp"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

var domainLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validDomainName(name string) bool {
	if len(name) > 253 || name != strings.ToLower(strings.TrimSpace(name)) {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !domainLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

// 资产只声明绑定关系，证书可用状态由真实路由 TLS 校验写入。
func normalizeDomainAsset(collection string, input domain.Object, state domain.State, existing domain.Object) (domain.Object, error) {
	if collection != "domains" && collection != "certificates" && collection != "resolvers" && collection != "ingresses" {
		return input, nil
	}
	result := cloneObject(input)
	name := strings.TrimSpace(stringValue(result["name"]))
	if name == "" {
		return nil, errors.New("DOMAIN_ASSET_NAME_REQUIRED")
	}
	result["name"] = name
	switch collection {
	case "domains":
		for _, field := range []string{"redirect", "redirectRuntime"} {
			if routeRevision(result[field]) != routeRevision(existing[field]) {
				return nil, errors.New("DOMAIN_REDIRECT_READ_ONLY")
			}
		}
		if _, old := result["httpsRedirectEnabled"]; old {
			return nil, errors.New("DOMAIN_REDIRECT_USE_RULE")
		}
		name = strings.ToLower(name)
		if !validDomainName(name) {
			return nil, errors.New("DOMAIN_NAME_INVALID")
		}
		result["name"] = name
		if existing != nil && (existing["name"] != name || stringValue(existing["certificateId"]) != stringValue(result["certificateId"])) {
			if err := domainRedirectReferences(state, stringValue(existing["id"])); err != nil {
				return nil, err
			}
		}
		if id := stringValue(result["certificateId"]); id != "" {
			certificate := findObject(state.Certificates, id)
			if certificate == nil || stringValue(certificate["issuer"]) != "lets_encrypt" || !containsString(objectStringSlice(certificate["domains"]), name) {
				return nil, errors.New("DOMAIN_CERTIFICATE_INVALID")
			}
		}
	case "certificates":
		if stringValue(result["issuer"]) != "lets_encrypt" || stringValue(result["challengeType"]) != "http-01" {
			return nil, errors.New("CERTIFICATE_ISSUER_UNSUPPORTED")
		}
		for _, key := range []string{"certificatePem", "privateKeyPem", "crtSecretRef", "keySecretRef"} {
			if _, exists := result[key]; exists {
				return nil, errors.New("CERTIFICATE_UPLOAD_UNSUPPORTED")
			}
		}
		domains, seen := []string{}, map[string]bool{}
		for _, raw := range objectStringSlice(result["domains"]) {
			host := strings.ToLower(strings.TrimSpace(raw))
			if !validDomainName(host) {
				return nil, errors.New("CERTIFICATE_DOMAIN_INVALID")
			}
			if !seen[host] {
				domains = append(domains, host)
				seen[host] = true
			}
		}
		if len(domains) == 0 {
			return nil, errors.New("CERTIFICATE_DOMAIN_REQUIRED")
		}
		if findObject(state.Resolvers, stringValue(result["resolverId"])) == nil {
			return nil, errors.New("CERTIFICATE_RESOLVER_REQUIRED")
		}
		result["domains"], result["status"], result["renewalStatus"] = domains, "pending", "managed"
	case "resolvers":
		if findObject(state.Ingresses, stringValue(result["ingressId"])) == nil {
			return nil, errors.New("RESOLVER_INGRESS_REQUIRED")
		}
	case "ingresses":
		ids := objectStringSlice(result["serverIds"])
		if len(ids) == 0 {
			return nil, errors.New("INGRESS_SERVER_REQUIRED")
		}
		for _, id := range ids {
			if findObject(state.Servers, id) == nil {
				return nil, errors.New("INGRESS_SERVER_NOT_FOUND")
			}
		}
	}
	items, _ := collectionRef(&state, collection)
	for _, item := range *items {
		if stringValue(item["id"]) != stringValue(existing["id"]) && strings.EqualFold(stringValue(item["name"]), stringValue(result["name"])) {
			return nil, errors.New("DOMAIN_ASSET_NAME_CONFLICT")
		}
	}
	if existing != nil && domainAssetHasActiveRoute(state, collection, stringValue(existing["id"])) {
		for _, key := range []string{"name", "certificateId", "issuer", "challengeType", "domains", "resolverId", "ingressId", "serverIds"} {
			if routeRevision(existing[key]) != routeRevision(result[key]) {
				return nil, errors.New("DOMAIN_ASSET_HAS_ACTIVE_ROUTE")
			}
		}
	}
	return result, nil
}

func domainAssetHasActiveRoute(state domain.State, collection, id string) bool {
	routes := append([]domain.Object(nil), state.Routes...)
	for _, server := range state.Servers {
		for _, binding := range domain.JenkinsAccessSnapshots(server) {
			bound := domain.Object{"id": binding["domainId"], "certificateId": binding["certificateId"]}
			routes = append(routes, domain.Object{"lastApplied": domain.Object{"domains": []domain.Object{bound}}})
		}
	}
	for _, source := range state.Domains {
		runtime := objectMapFromAny(source["redirectRuntime"])
		if len(possibleRouteSnapshots(runtime)) > 0 {
			routes = append(routes, runtime)
		}
	}
	for _, route := range routes {
		snapshots := []domain.Object{objectMapFromAny(route["lastApplied"]), objectMapFromAny(route["pendingRemote"])}
		if appID := stringValue(route["applicationId"]); appID != "" && applicationOperationActive(state, appID) != nil {
			snapshots = append(snapshots, route)
		}
		for _, snapshot := range snapshots {
			for _, bound := range objectSlice(snapshot["domains"]) {
				if collection == "domains" && stringValue(bound["id"]) == id {
					return true
				}
				certificateID := stringValue(bound["certificateId"])
				if collection == "certificates" && certificateID == id {
					return true
				}
				certificate := findObject(state.Certificates, certificateID)
				resolverID := stringValue(certificate["resolverId"])
				if collection == "resolvers" && resolverID == id {
					return true
				}
				resolver := findObject(state.Resolvers, resolverID)
				if collection == "ingresses" && stringValue(resolver["ingressId"]) == id {
					return true
				}
			}
		}
	}
	return false
}

func validateDomainAssetDeletion(state domain.State, collection, id string) error {
	if collection == "domains" {
		if err := domainRedirectReferences(state, id); err != nil {
			return err
		}
		if err := redirectOperationActive(state, id); err != nil {
			return err
		}
	}
	if domainAssetHasActiveRoute(state, collection, id) {
		return errors.New("DOMAIN_ASSET_HAS_ACTIVE_ROUTE")
	}
	switch collection {
	case "domains":
		for _, route := range state.Routes {
			for _, asset := range objectSlice(route["domains"]) {
				if stringValue(asset["id"]) == id {
					return errors.New("DOMAIN_HAS_ROUTE")
				}
			}
		}
	case "certificates":
		if findObjectBy(state.Domains, "certificateId", id) != nil {
			return errors.New("CERTIFICATE_HAS_DOMAIN")
		}
	case "resolvers":
		if findObjectBy(state.Certificates, "resolverId", id) != nil {
			return errors.New("RESOLVER_HAS_CERTIFICATE")
		}
	case "ingresses":
		// 分组删除会级联清理自己的签发入口；仍被证书引用时必须先解除引用。
		for _, resolver := range state.Resolvers {
			if stringValue(resolver["ingressId"]) != id {
				continue
			}
			if findObjectBy(state.Certificates, "resolverId", stringValue(resolver["id"])) != nil {
				return errors.New("INGRESS_HAS_CERTIFICATE")
			}
		}
	}
	return nil
}

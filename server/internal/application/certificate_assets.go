package application

import (
	"context"
	"errors"
	"sort"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 受管网关分组与 HTTP-01 签发入口由证书域名与所选网关服务器派生，
// 界面不需要用户分别维护 Ingress 和 Resolver 两级对象。
const (
	managedIngressName  = "受管网关"
	managedResolverName = "HTTP-01 自动签发"
)

// CreateCertificateAsset 创建证书资产，并在未指定签发入口时按网关服务器派生入口。
func (r *Runtime) CreateCertificateAsset(ctx context.Context, input domain.Object) (domain.Object, error) {
	serverIDs := objectStringSlice(input["serverIds"])
	resolverID := stringValue(input["resolverId"])
	certificate := domain.Object{}
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		if resolverID == "" {
			if len(serverIDs) == 0 {
				return errors.New("CERTIFICATE_GATEWAY_SERVER_REQUIRED")
			}
			for _, id := range serverIDs {
				if findObject(state.Servers, id) == nil {
					return errors.New("INGRESS_SERVER_NOT_FOUND")
				}
			}
			_, resolver, err := ensureManagedVerificationEntry(state, serverIDs)
			if err != nil {
				return err
			}
			resolverID = stringValue(resolver["id"])
		}
		now := domain.Now()
		item := cloneObject(input)
		delete(item, "serverIds")
		item["resolverId"] = resolverID
		item["issuer"] = firstNonEmptyString(stringValue(item["issuer"]), "lets_encrypt")
		item["challengeType"] = firstNonEmptyString(stringValue(item["challengeType"]), "http-01")
		item["id"] = domain.NewID("cert")
		item["createdAt"], item["updatedAt"] = now, now
		normalized, err := normalizeDomainAsset("certificates", normalizeCreatedObject("certificates", item, state), *state, nil)
		if err != nil {
			return err
		}
		state.Certificates = append(state.Certificates, normalized)
		certificate = cloneObject(normalized)
		state.OperationLogs = append(state.OperationLogs, newOperationLog("create_certificates", "certificates", stringValue(certificate["id"]), "succeeded", "已创建证书记录", ""))
		return nil
	})
	return certificate, err
}

func (r *Runtime) UpdateCertificateAsset(ctx context.Context, id string, input domain.Object) (domain.Object, error) {
	serverIDs := objectStringSlice(input["serverIds"])
	certificate := domain.Object{}
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		existing := findObject(state.Certificates, id)
		if existing == nil {
			return errors.New("CERTIFICATE_NOT_FOUND")
		}
		item := cloneObject(input)
		delete(item, "serverIds")
		item["issuer"] = firstNonEmptyString(stringValue(item["issuer"]), "lets_encrypt")
		item["challengeType"] = firstNonEmptyString(stringValue(item["challengeType"]), "http-01")
		if len(serverIDs) > 0 {
			for _, serverID := range serverIDs {
				if findObject(state.Servers, serverID) == nil {
					return errors.New("INGRESS_SERVER_NOT_FOUND")
				}
			}
			_, resolver, err := ensureManagedVerificationEntry(state, serverIDs)
			if err != nil {
				return err
			}
			item["resolverId"] = stringValue(resolver["id"])
		}
		next := cloneObject(existing)
		for key, value := range domain.CloneObjectWithoutUserReferences(item) {
			if key == "id" || key == "createdAt" {
				continue
			}
			next[key] = value
		}
		next["updatedAt"] = domain.Now()
		normalized, err := normalizeDomainAsset("certificates", next, *state, existing)
		if err != nil {
			return err
		}
		updateStateObject(&state.Certificates, id, normalized)
		certificate = cloneObject(normalized)
		state.OperationLogs = append(state.OperationLogs, newOperationLog("update_certificates", "certificates", id, "succeeded", "已更新证书记录", ""))
		return nil
	})
	return certificate, err
}

// ensureManagedVerificationEntry 复用同一组网关服务器对应的受管入口，避免重复对象。
func ensureManagedVerificationEntry(state *domain.State, serverIDs []string) (domain.Object, domain.Object, error) {
	ingress := findManagedIngress(state, serverIDs)
	if ingress == nil {
		now := domain.Now()
		ingress = domain.Object{
			"id": domain.NewID("ingress"), "name": managedIngressName,
			"serverIds": sortedUniqueServerIDs(serverIDs),
			"createdAt": now, "updatedAt": now,
		}
		state.Ingresses = append(state.Ingresses, ingress)
	}
	resolver := findObjectBy(state.Resolvers, "ingressId", stringValue(ingress["id"]))
	if resolver == nil {
		now := domain.Now()
		resolver = domain.Object{
			"id": domain.NewID("resolver"), "name": managedResolverName,
			"ingressId": stringValue(ingress["id"]),
			"createdAt": now, "updatedAt": now,
		}
		state.Resolvers = append(state.Resolvers, resolver)
	}
	return ingress, resolver, nil
}

// 网关服务器集合完全一致时视为同一入口，保证同一组网关只暴露一份验证通道。
func findManagedIngress(state *domain.State, serverIDs []string) domain.Object {
	expected := sortedUniqueServerIDs(serverIDs)
	for _, ingress := range state.Ingresses {
		if stringValue(ingress["name"]) != managedIngressName {
			continue
		}
		if equalStringSlices(sortedUniqueServerIDs(objectStringSlice(ingress["serverIds"])), expected) {
			return ingress
		}
	}
	return nil
}

// 网关集合按去重排序比较，顺序不同不应产生重复入口。
func sortedUniqueServerIDs(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := value
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	sort.Strings(result)
	return result
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// 删除证书时清理不再被任何证书引用的签发入口，以及它独占的受管网关分组。
func cascadeCertificateDeletion(state *domain.State, certificate domain.Object) {
	resolverID := stringValue(certificate["resolverId"])
	if resolverID == "" {
		return
	}
	if findObjectBy(state.Certificates, "resolverId", resolverID) != nil {
		return
	}
	ingressID := ""
	nextResolvers := make([]domain.Object, 0, len(state.Resolvers))
	for _, resolver := range state.Resolvers {
		if stringValue(resolver["id"]) == resolverID {
			ingressID = stringValue(resolver["ingressId"])
			continue
		}
		nextResolvers = append(nextResolvers, resolver)
	}
	state.Resolvers = nextResolvers
	cascadeOrphanIngress(state, ingressID)
}

// 删除网关分组时级联清理其签发入口；仍被证书引用时必须先解除引用。
func cascadeIngressDeletion(state *domain.State, ingressID string) error {
	removed := map[string]bool{}
	for _, resolver := range state.Resolvers {
		if stringValue(resolver["ingressId"]) != ingressID {
			continue
		}
		if findObjectBy(state.Certificates, "resolverId", stringValue(resolver["id"])) != nil {
			return errors.New("INGRESS_RESOLVER_REFERENCED_BY_CERTIFICATE")
		}
		removed[stringValue(resolver["id"])] = true
	}
	if len(removed) == 0 {
		return nil
	}
	next := make([]domain.Object, 0, len(state.Resolvers))
	for _, resolver := range state.Resolvers {
		if !removed[stringValue(resolver["id"])] {
			next = append(next, resolver)
		}
	}
	state.Resolvers = next
	return nil
}

// 删除签发入口后清理不再被引用的受管网关分组，用户手工分组保留原样。
func cascadeOrphanIngress(state *domain.State, ingressID string) {
	if ingressID == "" {
		return
	}
	if ingress := findObject(state.Ingresses, ingressID); ingress == nil || stringValue(ingress["name"]) != managedIngressName {
		return
	}
	for _, other := range state.Resolvers {
		if stringValue(other["ingressId"]) == ingressID {
			return
		}
	}
	next := make([]domain.Object, 0, len(state.Ingresses))
	for _, ingress := range state.Ingresses {
		if stringValue(ingress["id"]) != ingressID {
			next = append(next, ingress)
		}
	}
	state.Ingresses = next
}

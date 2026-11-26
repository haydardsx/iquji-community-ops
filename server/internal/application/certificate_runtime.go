package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 证书由受管 Traefik 的 ACME 解析器真实签发；本层只触发并校验远端结果。
type CertificateVerifier interface {
	Verify(context.Context, CertificateVerifyInput) (domain.Object, error)
}

type CertificateVerifyInput struct {
	Certificate  domain.Object
	Domains      []string
	Server       domain.Object
	SSHKey       string
	TraefikName  string
	ResolverName string
	HTTPSPort    int
	HTTPPort     int
	Network      string
	ClusterID    string
	Log          func(string, string, string, string)
}

// VerifyCertificate 在真实网关上触发 ACME 签发并读取对端证书事实。
func (r *Runtime) VerifyCertificate(ctx context.Context, id string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	certificate := findObject(state.Certificates, id)
	if certificate == nil {
		return nil, errors.New("CERTIFICATE_NOT_FOUND")
	}
	if stringValue(certificate["issuer"]) != "lets_encrypt" || stringValue(certificate["challengeType"]) != "http-01" {
		return nil, errors.New("CERTIFICATE_ISSUER_UNSUPPORTED")
	}
	domains := objectStringSlice(certificate["domains"])
	if len(domains) == 0 {
		return nil, errors.New("CERTIFICATE_DOMAIN_REQUIRED")
	}
	resolver := findObject(state.Resolvers, stringValue(certificate["resolverId"]))
	if resolver == nil {
		return nil, errors.New("CERTIFICATE_RESOLVER_REQUIRED")
	}
	ingress := findObject(state.Ingresses, stringValue(resolver["ingressId"]))
	if ingress == nil {
		return nil, errors.New("RESOLVER_INGRESS_REQUIRED")
	}
	serverIDs := objectStringSlice(ingress["serverIds"])
	if len(serverIDs) == 0 {
		return nil, errors.New("INGRESS_SERVER_REQUIRED")
	}
	if r.CertificateVerifier == nil {
		return nil, errors.New("CERTIFICATE_VERIFIER_NOT_CONFIGURED")
	}
	results := []domain.Object{}
	for _, serverID := range serverIDs {
		if err := r.ensureNoSwarmMaintenance(ctx, serverID); err != nil {
			return nil, err
		}
		server := findObject(state.Servers, serverID)
		if server == nil {
			return nil, errors.New("INGRESS_SERVER_NOT_FOUND")
		}
		sshKey, ok := r.serverSSHSecretMaterial(ctx, state, server)
		if !ok {
			return nil, errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
		}
		result, verifyErr := r.CertificateVerifier.Verify(ctx, CertificateVerifyInput{
			Certificate:  cloneObject(certificate),
			Domains:      domains,
			Server:       cloneObject(server),
			SSHKey:       sshKey,
			TraefikName:  r.Config.TraefikService,
			ResolverName: managedTraefikResolver(r.Config.TraefikCertResolver),
			HTTPSPort:    r.Config.TraefikHTTPSPort,
			HTTPPort:     r.Config.TraefikHTTPPort,
			Network:      r.Config.SwarmNetwork,
			ClusterID:    stringValue(serverRuntimeMetrics(server)["swarmClusterId"]),
			Log: func(source, phase, level, message string) {
				if task, ok := domain.TaskClaimFromContext(ctx); ok {
					r.appendRuntimeMessage(task, source, phase, level, message)
				}
			},
		})
		if verifyErr != nil {
			code := stableErrorCode(verifyErr, "CERTIFICATE_VERIFY_FAILED")
			_, _ = r.updateCertificateState(context.WithoutCancel(ctx), id, domain.Object{
				"status": "failed", "renewalStatus": "pending", "lastErrorCode": code,
				"lastErrorSummary": "网关签发校验未通过，请检查 DNS 指向与 80/443 端口可达性。",
				"lastVerifiedAt":   domain.Now(),
			})
			return nil, errors.New(code)
		}
		results = append(results, result)
	}
	expiresAt, issuer := earliestExpiry(results)
	updated, err := r.updateCertificateState(ctx, id, domain.Object{
		"status": "valid", "renewalStatus": "managed", "issuer": "lets_encrypt",
		"lastVerifiedAt": domain.Now(), "expiresAt": expiresAt,
		"lastErrorCode": "", "lastErrorSummary": "",
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"certificate": updated,
		"issuerName":  issuer,
		"verified":    results,
	}, nil
}

func (r *Runtime) updateCertificateState(ctx context.Context, id string, patch domain.Object) (domain.Object, error) {
	result := domain.Object{}
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		item := findObject(state.Certificates, id)
		if item == nil {
			return errors.New("CERTIFICATE_NOT_FOUND")
		}
		for key, value := range patch {
			item[key] = value
		}
		item["updatedAt"] = domain.Now()
		updateStateObject(&state.Certificates, id, item)
		result = cloneObject(item)
		return nil
	})
	return result, err
}

// 多网关时必须全部通过；到期时间取最早值，避免用单节点掩盖过期风险。
func earliestExpiry(results []domain.Object) (string, string) {
	expiresAt, issuer := "", ""
	for _, result := range results {
		value := stringValue(result["expiresAt"])
		if value != "" && (expiresAt == "" || value < expiresAt) {
			expiresAt = value
		}
		if issuer == "" {
			issuer = stringValue(result["issuerName"])
		}
	}
	return expiresAt, issuer
}

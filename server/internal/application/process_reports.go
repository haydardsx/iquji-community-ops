package application

import (
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func processResponse(report domain.CheckReport, payloadName string, payload any) map[string]any {
	return map[string]any{
		"process":   report,
		payloadName: payload,
	}
}

func legacyRuntimeProcess(kind string, subject domain.ResourceRef, phase string, result domain.Object) domain.CheckReport {
	metrics := objectMapFromAny(result["metrics"])
	builder := domain.NewCheckReportBuilder(kind, subject).Metrics(metrics)
	for _, check := range objectSlice(result["checks"]) {
		code := processCheckCode(firstNonEmptyString(stringValue(check["code"]), stringValue(check["name"]), stringValue(check["summary"])))
		outcome, severity, blocking := legacyCheckStatus(stringValue(check["status"]))
		builder.Check(
			code,
			firstNonEmptyString(stringValue(check["category"]), stringValue(check["name"]), "runtime"),
			firstNonEmptyString(stringValue(check["phase"]), phase),
			subject,
			outcome,
			severity,
			blocking,
			stringValue(check["summary"]),
			objectMapFromAny(check["details"]),
		)
	}
	return builder.Report()
}

func processCheckCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "PROCESS_CHECK"
	}
	replacer := strings.NewReplacer(" ", "_", "-", "_", ":", "_", "/", "_", ".", "_")
	return strings.ToUpper(replacer.Replace(value))
}

func legacyCheckStatus(status string) (outcome string, severity string, blocking bool) {
	switch status {
	case serverRuntimeCheckSuccess:
		return domain.CheckOutcomePassed, domain.CheckSeverityInfo, false
	case serverRuntimeCheckWarning:
		return domain.CheckOutcomeWarning, domain.CheckSeverityWarning, false
	case serverRuntimeCheckDanger:
		return domain.CheckOutcomeFailed, domain.CheckSeverityDanger, true
	case serverRuntimeCheckSkipped:
		return domain.CheckOutcomeSkipped, domain.CheckSeverityInfo, false
	default:
		return domain.CheckOutcomeSkipped, domain.CheckSeverityWarning, false
	}
}

func payloadWithoutWorkflowFields(input domain.Object) domain.Object {
	payload := cloneObject(input)
	for _, key := range []string{
		"checks",
		"blockedReasons",
		"blockingIssues",
		"warnings",
		"canStart",
		"canDelete",
		"hasBlockingIssues",
	} {
		delete(payload, key)
	}
	return payload
}

func processDecisionAllowed(value any) bool {
	if report, ok := value.(domain.CheckReport); ok {
		return report.Decision.Allowed
	}
	process := objectMapFromAny(value)
	decision := objectMapFromAny(process["decision"])
	allowed, ok := decision["allowed"].(bool)
	return ok && allowed
}

func processBlockers(value any) []string {
	if report, ok := value.(domain.CheckReport); ok {
		return append([]string(nil), report.Decision.Blockers...)
	}
	process := objectMapFromAny(value)
	decision := objectMapFromAny(process["decision"])
	return objectStringSlice(decision["blockers"])
}

func resourceRef(kind string, id string, name string) domain.ResourceRef {
	return domain.Resource(kind, id, firstNonEmptyString(name, id))
}

func deleteResourceKind(impactKey string) string {
	switch impactKey {
	case "projects":
		return "project"
	case "applications":
		return "application"
	case "releasePlans":
		return "release_plan"
	case "servers":
		return "server"
	case "secretAssets":
		return "secret_asset"
	case "routes":
		return "route"
	case "domains":
		return "domain"
	case "certificates":
		return "certificate"
	case "resolvers":
		return "resolver"
	case "ingresses":
		return "ingress"
	case "frpTunnels":
		return "frp_tunnel"
	case "frpTunnelRuns":
		return "frp_tunnel_run"
	default:
		return impactKey
	}
}

func summaryCheck(builder *domain.CheckReportBuilder, code string, category string, phase string, target domain.ResourceRef, passed bool, blocking bool, summary string) {
	outcome := domain.CheckOutcomePassed
	severity := domain.CheckSeverityInfo
	if !passed {
		outcome = domain.CheckOutcomeFailed
		severity = domain.CheckSeverityDanger
	}
	builder.Check(code, category, phase, target, outcome, severity, blocking, summary, nil)
}

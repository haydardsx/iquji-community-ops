package application

import "github.com/haydardsx/iquji-community-ops/server/internal/domain"

// ReleasePlanPreviewBlockedError 保留稳定总码，并携带可展示的具体阻断项。
type ReleasePlanPreviewBlockedError struct {
	Blockers []string
	Checks   []domain.CheckItem
}

func (e *ReleasePlanPreviewBlockedError) Error() string {
	return "RELEASE_PLAN_PREVIEW_BLOCKED"
}

func newReleasePlanPreviewBlockedError(report domain.CheckReport) *ReleasePlanPreviewBlockedError {
	checks := []domain.CheckItem{}
	for _, check := range report.Checks {
		if check.Blocking && check.Outcome == domain.CheckOutcomeFailed {
			check.Details = releasePlanCheckDetails(check)
			checks = append(checks, check)
		}
	}
	return &ReleasePlanPreviewBlockedError{
		Blockers: append([]string{}, report.Decision.Blockers...),
		Checks:   checks,
	}
}

// releasePlanCheckDetails 只向客户端暴露预检展示所需的无秘密字段。
func releasePlanCheckDetails(check domain.CheckItem) domain.Object {
	result := domain.Object{}
	for _, key := range []string{"capability", "projectId", "requiredBundleRevision"} {
		if value, ok := check.Details[key]; ok {
			result[key] = value
		}
	}
	if check.Code == "SWARM_CLUSTER_MISMATCH" {
		for _, key := range []string{"deploymentManagerServerId", "deploymentManagerServerName", "deploymentClusterId", "serverClusterId"} {
			if value, ok := check.Details[key]; ok {
				result[key] = value
			}
		}
	}
	return result
}

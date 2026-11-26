package application

import (
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestReleasePlanPreviewBlockedErrorKeepsStableCodeAndSafeCheckDetails(t *testing.T) {
	report := domain.CheckReport{
		Decision: domain.CheckDecision{Blockers: []string{"SWARM_CLUSTER_MISMATCH", "GATEWAY_SERVER_SSH_UNAVAILABLE"}},
		Checks: []domain.CheckItem{
			{
				Code: "SWARM_CLUSTER_MISMATCH", Outcome: domain.CheckOutcomeFailed, Blocking: true,
				Details: domain.Object{
					"deploymentManagerServerId": "srv_gateway", "deploymentManagerServerName": "网关",
					"deploymentClusterId": "cluster_one", "serverClusterId": "cluster_two",
					"secretValue": "must-not-leak", "configSnapshot": domain.Object{"password": "secret"},
				},
			},
			{Code: "WARNING_ONLY", Outcome: domain.CheckOutcomeWarning, Blocking: false, Details: domain.Object{"secretValue": "ignored"}},
		},
	}
	err := newReleasePlanPreviewBlockedError(report)
	if err.Error() != "RELEASE_PLAN_PREVIEW_BLOCKED" {
		t.Fatalf("unexpected stable error code: %q", err.Error())
	}
	if len(err.Blockers) != 2 || len(err.Checks) != 1 {
		t.Fatal("unexpected blocker or check count")
	}
	details := err.Checks[0].Details
	if details["deploymentManagerServerName"] != "网关" || details["deploymentClusterId"] != "cluster_one" || details["serverClusterId"] != "cluster_two" {
		t.Fatal("cluster display details missing")
	}
	if _, ok := details["secretValue"]; ok {
		t.Fatal("secret value leaked into preview error details")
	}
	if _, ok := details["configSnapshot"]; ok {
		t.Fatal("full config leaked into preview error details")
	}
}

func TestReleasePlanPreviewBlockedErrorOnlyIncludesBlockingFailures(t *testing.T) {
	report := domain.CheckReport{Checks: []domain.CheckItem{
		{Code: "PASS", Outcome: domain.CheckOutcomePassed, Blocking: false},
		{Code: "WARNING", Outcome: domain.CheckOutcomeWarning, Blocking: false},
		{Code: "NON_BLOCKING_FAILURE", Outcome: domain.CheckOutcomeFailed, Blocking: false},
		{Code: "BLOCKING_FAILURE", Outcome: domain.CheckOutcomeFailed, Blocking: true},
	}}
	err := newReleasePlanPreviewBlockedError(report)
	if len(err.Checks) != 1 || err.Checks[0].Code != "BLOCKING_FAILURE" {
		t.Fatal("unexpected included checks")
	}
}

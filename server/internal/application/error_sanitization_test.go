package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestStableErrorCodeNeverReturnsRemoteCommandDetails(t *testing.T) {
	if actual := stableErrorCode(errors.New("dial tcp 127.0.0.1:58080: connect: connection refused"), "JENKINS_UNAVAILABLE"); actual != "JENKINS_UNAVAILABLE" {
		t.Fatal("raw connection failure was not sanitized")
	}
	if actual := stableErrorCode(errors.New("wrapped: SWARM_CLUSTER_MISMATCH: docker node inspect secret-node"), "SWARM_OPERATION_FAILED"); actual != "SWARM_CLUSTER_MISMATCH" {
		t.Fatal("stable nested code was not preserved")
	}
	if actual := stableErrorCode(errors.New("exit status 1"), "REMOTE_RUNTIME_FAILED"); actual != "REMOTE_RUNTIME_FAILED" {
		t.Fatal("raw exit status was not replaced")
	}
}

func TestStableErrorCodeUsesOnlyPublicDiagnostics(t *testing.T) {
	const fallback = "SWARM_OPERATION_FAILED"
	for _, tc := range []struct {
		name     string
		err      error
		fallback string
		want     string
	}{
		{"unknown identifier", errors.New("failure for EXAMPLE_TENANT_LABEL"), fallback, fallback},
		{"identifier with known suffix", errors.New("exampleSWARM_CLUSTER_MISMATCH"), fallback, fallback},
		{"wrapped diagnostic", fmt.Errorf("EXAMPLE_TENANT_LABEL: %w", errors.New("SWARM_CLUSTER_MISMATCH")), fallback, "SWARM_CLUSTER_MISMATCH"},
		{"插件包校验失败", errors.New("JENKINS_PLUGIN_BUNDLE_INVALID: example detail"), "JENKINS_RUNTIME_FAILED", "JENKINS_PLUGIN_BUNDLE_INVALID"},
		{"unregistered fallback", errors.New("operation unavailable"), "EXAMPLE_TENANT_LABEL", "OPERATION_FAILED"},
		{"nil with unregistered fallback", nil, "EXAMPLE_TENANT_LABEL", "OPERATION_FAILED"},
		{"canceled", fmt.Errorf("EXAMPLE_TENANT_LABEL: %w", context.Canceled), fallback, "OPERATION_CANCELLED"},
		{"deadline", fmt.Errorf("EXAMPLE_TENANT_LABEL: %w", context.DeadlineExceeded), fallback, "OPERATION_TIMEOUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stableErrorCode(tc.err, tc.fallback); got != tc.want {
				t.Fatal("unexpected public diagnostic")
			}
		})
	}
}

func TestJenkinsResultReportsOnlyPublicDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		run  domain.Object
		want string
	}{
		{domain.Object{"status": "failed", "summary": "example job output"}, "JENKINS_RELEASE_FAILED"},
		{domain.Object{"status": "failed", "summary": "JENKINS_IMAGE_BUILD_FAILED: example detail"}, "JENKINS_IMAGE_BUILD_FAILED"},
		{domain.Object{"status": "failed", "lastErrorSummary": "JENKINS_CALLBACK_TIMEOUT", "summary": "example job output"}, "JENKINS_CALLBACK_TIMEOUT"},
		{domain.Object{"status": "cancelled", "summary": "example job output"}, "JENKINS_RELEASE_CANCELLED"},
	} {
		message, err := jenkinsResult(tc.run)
		if message != "" || err == nil || err.Error() != tc.want {
			t.Fatal("unexpected public release result")
		}
	}
}

func TestPublicFrpRunSummariesHandleHistoricalErrors(t *testing.T) {
	for _, tc := range []struct {
		status  domain.FrpTunnelStatus
		summary string
		want    any
	}{
		{domain.FrpTunnelStatusRunning, "", nil},
		{domain.FrpTunnelStatusFailed, "example job output", "FRP_TUNNEL_OPERATION_FAILED"},
		{domain.FrpTunnelStatusCleanupRequired, "FRPC_PROCESS_EXITED: example detail", "FRPC_PROCESS_EXITED"},
		{domain.FrpTunnelStatusCancelled, "start cancelled", "OPERATION_CANCELLED"},
	} {
		run := domain.FrpTunnelRun{Status: tc.status, ErrorSummary: tc.summary}
		if publicFrpTunnelRun(run)["errorSummary"] != tc.want {
			t.Fatal("unexpected public tunnel run diagnostic")
		}
	}
}

func TestUnavailableApplicationReasonsExposeOnlyRegisteredCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errors.New("APPLICATION_NOT_FOUND: synthetic detail"), "APPLICATION_NOT_FOUND"},
		{errors.New("SWARM_CLUSTER_MISMATCH"), "SWARM_CLUSTER_MISMATCH"},
		{errors.New("APPLICATION_EXAMPLE_PRIVATE_DETAIL"), "APPLICATION_REMOTE_QUERY_FAILED"},
		{errors.New("SSH_ERROR example detail"), "APPLICATION_REMOTE_QUERY_FAILED"},
	} {
		if applicationUnavailableReason(tc.err) != tc.want {
			t.Fatal("unexpected public reason for application availability")
		}
	}
}

func TestSwarmObservationReturnsOnlyRegisteredDiagnostics(t *testing.T) {
	for _, tc := range []struct{ code, want string }{
		{"SWARM_CLUSTER_MISMATCH", "SWARM_CLUSTER_MISMATCH"},
		{"SWARM_EXAMPLE_DETAIL", "SWARM_PROBE_FAILED"},
		{"synthetic upstream error", "SWARM_PROBE_FAILED"},
	} {
		metrics := domain.Object{"swarmVerificationError": tc.code}
		if swarmIdentityReadinessCode(metrics) != tc.want {
			t.Fatal("unexpected readiness diagnostic")
		}
		snapshot := domain.Object{"status": "error", "errorCode": tc.code}
		if currentSwarmSnapshot(domain.Object{"id": "example"}, snapshot)["errorCode"] != tc.want {
			t.Fatal("unexpected snapshot diagnostic")
		}
	}
}

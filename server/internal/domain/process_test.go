package domain

import "testing"

func TestCheckReportBuilderCalculatesDecisionAndStatus(t *testing.T) {
	subject := Resource("server", "srv_1", "gateway")
	cases := []struct {
		name     string
		build    func(*CheckReportBuilder)
		status   string
		allowed  bool
		blockers int
		warnings int
	}{
		{
			name:    "empty passes",
			build:   func(*CheckReportBuilder) {},
			status:  CheckStatusPassed,
			allowed: true,
		},
		{
			name: "all checks passed",
			build: func(builder *CheckReportBuilder) {
				builder.Check("SSH_OK", "runtime", "probe", subject, CheckOutcomePassed, CheckSeverityInfo, true, "SSH reachable", nil)
			},
			status:  CheckStatusPassed,
			allowed: true,
		},
		{
			name: "warning check remains allowed",
			build: func(builder *CheckReportBuilder) {
				builder.Check("AGENT_STALE", "runtime", "probe", subject, CheckOutcomeWarning, CheckSeverityWarning, false, "agent stale", nil)
			},
			status:   CheckStatusWarning,
			allowed:  true,
			warnings: 1,
		},
		{
			name: "blocking failure blocks",
			build: func(builder *CheckReportBuilder) {
				builder.Check("DOCKER_MISSING", "runtime", "probe", subject, CheckOutcomeFailed, CheckSeverityDanger, true, "docker missing", nil)
			},
			status:   CheckStatusBlocked,
			allowed:  false,
			blockers: 1,
		},
		{
			name: "non blocking failure warns",
			build: func(builder *CheckReportBuilder) {
				builder.Check("AGENT_MISSING", "runtime", "probe", subject, CheckOutcomeFailed, CheckSeverityDanger, false, "agent missing", nil)
			},
			status:   CheckStatusWarning,
			allowed:  true,
			warnings: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			builder := NewCheckReportBuilder(CheckKindRuntimeProbe, subject)
			tc.build(builder)
			report := builder.Report()
			if report.Status != tc.status {
				t.Fatalf("status = %q, want %q", report.Status, tc.status)
			}
			if report.Decision.Allowed != tc.allowed {
				t.Fatalf("allowed = %v, want %v", report.Decision.Allowed, tc.allowed)
			}
			if len(report.Decision.Blockers) != tc.blockers {
				t.Fatalf("blockers = %#v, want %d", report.Decision.Blockers, tc.blockers)
			}
			if len(report.Decision.Warnings) != tc.warnings {
				t.Fatalf("warnings = %#v, want %d", report.Decision.Warnings, tc.warnings)
			}
		})
	}
}

func TestCheckObjectUsesCanonicalFields(t *testing.T) {
	report := NewCheckReportBuilder(CheckKindDeleteImpact, Resource("server", "srv_1", "gateway")).
		Impact(ResourceActionDelete, Resource("server", "srv_1", "gateway"), 1, CheckSeverityDanger, "DELETE_SERVER").
		Report()
	value := CheckObject(report)
	for _, legacy := range []string{"blockedReasons", "blockingIssues", "warnings", "canStart", "canDelete", "hasBlockingIssues"} {
		if _, ok := value[legacy]; ok {
			t.Fatalf("process object leaked legacy top-level field %q", legacy)
		}
	}
	if value["decision"] == nil || value["impacts"] == nil || value["resources"] == nil {
		t.Fatalf("process object missing canonical fields: %#v", value)
	}
}

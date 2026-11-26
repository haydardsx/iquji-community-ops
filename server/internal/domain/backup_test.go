package domain

import (
	"errors"
	"net/url"
	"reflect"
	"testing"
)

func TestNormalizeBackupScopesRejectsInvalidSelections(t *testing.T) {
	if _, err := NormalizeBackupScopes(nil); !errors.Is(err, ErrBackupScopeRequired) {
		t.Fatalf("expected required scope error, got %v", err)
	}
	if _, err := NormalizeBackupScopes([]BackupScope{BackupScopeProjects, BackupScopeProjects}); !errors.Is(err, ErrBackupScopeDuplicate) {
		t.Fatalf("expected duplicate scope error, got %v", err)
	}
	if _, err := NormalizeBackupScopes([]BackupScope{"unknown"}); !errors.Is(err, ErrBackupScopeUnknown) {
		t.Fatalf("expected unknown scope error, got %v", err)
	}
	normalized, err := NormalizeBackupScopes([]BackupScope{BackupScopeFrpTunnels, BackupScopeProjects})
	if err != nil {
		t.Fatalf("normalize scopes: %v", err)
	}
	expected := []BackupScope{BackupScopeProjects, BackupScopeFrpTunnels}
	if !reflect.DeepEqual(normalized, expected) {
		t.Fatal("unexpected scope order")
	}
}

func TestResolveBackupScopeClosureFollowsTransitiveReferences(t *testing.T) {
	state := EmptyState()
	state.Projects = []Object{{"id": "prj_1", "serverIds": []string{"srv_app"}}}
	state.Applications = []Object{{
		"id": "app_1", "projectId": "prj_1",
		"sourceConfig": Object{"provider": "git", "authSecretRef": "secret_git"},
	}}
	state.ApplicationDeploymentConfigs = []Object{{
		"id": "cfg_1", "applicationId": "app_1", "buildServerId": "srv_build", "gatewayServerId": "srv_app",
		"placement": Object{"eligibleServerIds": []string{"srv_app"}},
	}}
	state.Routes = []Object{{
		"id": "route_1", "applicationId": "app_1",
		"domains": []Object{{"id": "domain_1", "certificateId": "cert_1"}},
	}}
	state.Domains = []Object{{"id": "domain_1", "certificateId": "cert_1"}}
	state.Certificates = []Object{{"id": "cert_1", "privateKeySecretRef": "secret_cert"}}
	state.Ingresses = []Object{{"id": "ing_1", "serverIds": []string{"srv_ingress"}}}
	state.Servers = []Object{
		{"id": "srv_app", "secretAssetIds": []string{"asset_ssh"}},
		{"id": "srv_build"},
		{"id": "srv_ingress"},
	}
	state.SecretAssets = []Object{
		{"id": "asset_ssh", "secretRef": "secret_ssh"},
		{"id": "asset_git", "secretRef": "secret_git"},
		{"id": "asset_cert", "secretRef": "secret_cert"},
	}

	effective, dependencies, err := ResolveBackupScopeClosure(state, []BackupScope{BackupScopeProjects})
	if err != nil {
		t.Fatalf("resolve closure: %v", err)
	}
	expected := []BackupScope{BackupScopeProjects, BackupScopeServers, BackupScopeDomainsCertificates, BackupScopeSecretAssets}
	if !reflect.DeepEqual(effective, expected) {
		t.Fatal("unexpected effective scopes")
	}
	reasons := map[string]bool{}
	for _, dependency := range dependencies {
		reasons[dependency.Reason] = true
	}
	for _, reason := range []string{
		"PROJECT_SERVER_REFERENCE",
		"PROJECT_ROUTE_DOMAIN_REFERENCE",
		"PROJECT_SECRET_REFERENCE",
		"INGRESS_SERVER_REFERENCE",
		"CERTIFICATE_SECRET_REFERENCE",
		"SERVER_SECRET_BINDING",
	} {
		if !reasons[reason] {
			t.Fatalf("missing dependency reason %s", reason)
		}
	}
}

func TestResolveBackupScopeClosureRecognizesTargetServers(t *testing.T) {
	state := EmptyState()
	state.ApplicationDeploymentConfigs = []Object{{
		"id":            "cfg_1",
		"applicationId": "app_1",
		"targetServers": []string{"srv_target"},
	}}
	state.Servers = []Object{{"id": "srv_target"}}

	effective, dependencies, err := ResolveBackupScopeClosure(state, []BackupScope{BackupScopeProjects})
	if err != nil {
		t.Fatalf("resolve closure: %v", err)
	}
	if !reflect.DeepEqual(effective, []BackupScope{BackupScopeProjects, BackupScopeServers}) {
		t.Fatal("targetServers did not add the server scope")
	}
	if len(dependencies) != 1 || dependencies[0].Reason != "PROJECT_SERVER_REFERENCE" {
		t.Fatal("unexpected targetServers dependency")
	}
}

func TestBuildBackupPayloadNormalizesLogicalDataAndHistory(t *testing.T) {
	state := EmptyState()
	state.Projects = []Object{{"id": "prj_1", "name": "Project"}}
	state.Applications = []Object{{
		"id": "app_1", "projectId": "prj_1", "status": "running",
		"sourceConfig": Object{
			"provider": "upload", "artifactAssetId": "upload_1", "artifactFileName": "source.tar.gz",
			"artifactSha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "artifactSizeBytes": 42,
		},
	}}
	state.ApplicationDeploymentConfigs = []Object{{"id": "cfg_1", "applicationId": "app_1"}}
	state.Routes = []Object{{
		"id": "route_1", "applicationId": "app_1", "status": "published",
		"domains":        []Object{{"id": "domain_1", "name": "example.test"}},
		"accessibleUrls": []string{"https://example.test/"},
	}}
	state.Servers = []Object{{"id": "srv_1", "status": "healthy", "health": Object{"ssh": true}}}
	state.Domains = []Object{{"id": "domain_1", "certificateId": "cert_1"}}
	state.Certificates = []Object{{
		"id": "cert_1", "certificatePem": "public-pem", "privateKeyPem": "private-pem", "privateKeySecretRef": "secret_cert",
	}}
	state.SecretAssets = []Object{{"id": "asset_cert", "secretRef": "secret_cert", "maskedSummary": "********"}}
	state.FrpTunnels = []FrpTunnel{{ID: "tun_1", Name: "Tunnel", ServerID: "srv_1"}}
	state.FrpTunnelRuns = []FrpTunnelRun{{ID: "run_1", TunnelID: "tun_1", TokenSecretRef: "secret_frp", Status: FrpTunnelStatusRunning}}
	state.RuntimeLogs = []RuntimeLog{
		{ID: "log_done", OperationRunID: "operation_done", TargetKind: "application", TargetID: "app_1"},
		{ID: "log_other", OperationRunID: "operation_other", TargetKind: "application", TargetID: "unselected"},
	}
	state.OperationLogs = []OperationLog{
		{ID: "operation_done", OperationRunID: "operation_done", TargetKind: "application", TargetID: "app_1"},
		{ID: "operation_other", OperationRunID: "operation_other", TargetKind: "application", TargetID: "unselected"},
	}

	payload := BuildBackupPayload(state, "schema-v1", BackupScopes(), true)
	if payload.Projects == nil || len(payload.Projects.Routes) != 1 {
		t.Fatal("project payload missing")
	}
	route := payload.Projects.Routes[0]
	if _, exists := route["domains"]; exists {
		t.Fatal("route retained embedded domain objects")
	}
	if got := StringSliceFromAny(route["domainIds"]); !reflect.DeepEqual(got, []string{"domain_1"}) {
		t.Fatal("unexpected route domain ids")
	}
	if _, exists := payload.Projects.Applications[0]["status"]; exists {
		t.Fatal("application runtime status leaked into backup")
	}
	if len(payload.Projects.UploadFiles) != 1 || payload.Projects.UploadFiles[0].ArchivePath != "files/uploads/upload_1" {
		t.Fatal("upload metadata missing")
	}
	certificate := payload.DomainsCertificates.Certificates[0]
	if _, exists := certificate["certificatePem"]; exists {
		t.Fatal("certificate PEM leaked into logical payload")
	}
	if _, exists := certificate["privateKeyPem"]; exists {
		t.Fatal("certificate private key leaked into logical payload")
	}
	if payload.FrpTunnels == nil || len(payload.FrpTunnels.Tunnels) != 1 {
		t.Fatal("FRP definition missing")
	}
	if payload.LogsAndAudit == nil || len(payload.LogsAndAudit.OperationLogs) != 1 || len(payload.LogsAndAudit.RuntimeLogs) != 1 {
		t.Fatal("operation history was not scoped to selected targets")
	}
	refs := BackupPayloadSecretRefs(payload)
	if !reflect.DeepEqual(refs, []string{"secret_cert"}) {
		t.Fatal("unexpected secret refs")
	}
}

func TestBackupFiltersLegacyRegistryMirrorValues(t *testing.T) {
	base := url.URL{Scheme: "https", Host: "mirror.example.test", Path: "/registry/"}
	withUser := base
	withUser.User = url.UserPassword("example", "sample")
	withQuery := base
	withQuery.RawQuery = url.Values{"key": {"sample"}}.Encode()
	withFragment := base
	withFragment.Fragment = "sample"
	for _, tc := range []struct {
		name  string
		value any
		keep  bool
	}{
		{"valid", base.String(), true},
		{"empty", "", true},
		{"userinfo", withUser.String(), false},
		{"query", withQuery.String(), false},
		{"fragment", withFragment.String(), false},
		{"malformed", []string{"sample"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := Object{"dockerRegistryMirrorUrl": tc.value, "architecture": "arm64"}
			state := EmptyState()
			state.Servers = []Object{{"id": "server", "platformProfile": profile}}
			payload := BuildBackupPayload(state, "schema-v1", []BackupScope{BackupScopeServers}, false)
			result := ObjectFromAny(payload.Servers.Servers[0]["platformProfile"])
			value, exists := result["dockerRegistryMirrorUrl"]
			if exists != tc.keep || exists && !reflect.DeepEqual(value, tc.value) || result["architecture"] != "arm64" {
				t.Fatal("backup did not preserve the expected public profile fields")
			}
			if !reflect.DeepEqual(profile["dockerRegistryMirrorUrl"], tc.value) {
				t.Fatal("backup changed the source profile")
			}
		})
	}
}

func TestBackupOperationalHistoryFiltersTerminalRelatedProcessAndFrpHits(t *testing.T) {
	reports := []CheckReport{
		{
			ID: "process_related", Kind: CheckKindTask, Status: CheckStatusPassed,
			Subject:   ResourceRef{Kind: "application", ID: "app_1"},
			Decision:  CheckDecision{Allowed: true, Blockers: []string{}, Warnings: []string{}},
			Checks:    []CheckItem{{ID: "check_1", Target: ResourceRef{Kind: "application", ID: "app_1"}, Details: Object{"password": "must-not-leak"}}},
			Resources: []ResourceRef{{Kind: "application", ID: "app_1"}}, Metrics: Object{}, GeneratedAt: Now(),
		},
		{ID: "process_running", Kind: CheckKindTask, Status: CheckStatusRunning, Subject: ResourceRef{Kind: "application", ID: "app_1"}, Decision: CheckDecision{}, Metrics: Object{}, GeneratedAt: Now()},
		{ID: "process_other", Kind: CheckKindTask, Status: CheckStatusFailed, Subject: ResourceRef{Kind: "application", ID: "app_other"}, Decision: CheckDecision{}, Metrics: Object{}, GeneratedAt: Now()},
	}
	filtered := FilterBackupCheckReports(reports, []string{"app_1"})
	if len(filtered) != 1 || filtered[0].ID != "process_related" {
		t.Fatal("unexpected process history filter")
	}
	if _, leaked := filtered[0].Checks[0].Details["password"]; leaked {
		t.Fatal("process history retained inline secret")
	}
	hits := FilterBackupFrpProbeHits([]FrpProbeHit{
		{ID: "hit_1", TunnelID: "tunnel_1", ReceivedAt: "2026-07-14T00:00:00Z"},
		{ID: "hit_2", TunnelID: "tunnel_other", ReceivedAt: "2026-07-14T00:00:01Z"},
	}, []string{"tunnel_1"})
	if len(hits) != 1 || hits[0].ID != "hit_1" {
		t.Fatal("unexpected FRP history filter")
	}
}

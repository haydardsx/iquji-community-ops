package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestVolumeDeclarationsRejectDuplicateAndEquivalentPaths(t *testing.T) {
	tests := []struct {
		name, expected string
		volumes        []domain.Object
	}{
		{"duplicate name", "APPLICATION_VOLUME_NAME_DUPLICATE", []domain.Object{{"name": "data", "target": "/data"}, {"name": "data", "target": "/backup"}}},
		{"duplicate target", "APPLICATION_VOLUME_TARGET_DUPLICATE", []domain.Object{{"name": "data", "target": "/data"}, {"name": "backup", "target": "/data"}}},
		{"normalized target", "APPLICATION_VOLUME_INVALID", []domain.Object{{"name": "data", "target": "/data"}, {"name": "backup", "target": "/data/../data"}}},
		{"root", "APPLICATION_VOLUME_INVALID", []domain.Object{{"name": "data", "target": "/"}}},
		{"noncanonical", "APPLICATION_VOLUME_INVALID", []domain.Object{{"name": "data", "target": "/data//files"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateVolumeDeclarations(domain.Object{"volumes": test.volumes})
			if err == nil || err.Error() != test.expected {
				t.Fatalf("got %v, want %s", err, test.expected)
			}
		})
	}
}

func TestVolumeClaimsPreventOtherApplicationsClaimingSamePhysicalName(t *testing.T) {
	state := domain.EmptyState()
	state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app-foo-bar", "swarmRuntime": domain.Object{"volumes": []domain.Object{{"name": "store", "target": "/data"}}}}}
	if err := validateVolumeClaims(state, "app", domain.Object{"volumes": []domain.Object{{"name": "store", "target": "/data"}}}); err != nil {
		t.Fatal(err)
	}
	if err := validateVolumeClaims(state, "app-foo", domain.Object{"volumes": []domain.Object{{"name": "bar-store", "target": "/data"}}}); err == nil || err.Error() != "APPLICATION_VOLUME_SOURCE_CONFLICT" {
		t.Fatalf("got %v", err)
	}
	candidate := domain.Object{"volumes": []domain.Object{{"name": "bar-store", "target": "/data"}}}
	app := domain.Object{"id": "app-foo", "projectId": domain.BuiltinProjectID}
	if err := normalizeApplicationConfiguration(state, app, domain.Object{"swarmRuntime": domain.CloneObject(candidate), "placement": domain.Object{}}, false); err == nil || err.Error() != "APPLICATION_VOLUME_SOURCE_CONFLICT" {
		t.Fatalf("configuration save got %v", err)
	}
	state.Applications = []domain.Object{app}
	state.ApplicationDeploymentConfigs = append(state.ApplicationDeploymentConfigs, domain.Object{"applicationId": "app-foo", "sourceConfig": domain.Object{"provider": "image", "image": "example:1"}, "swarmRuntime": domain.CloneObject(candidate)})
	if _, err := applicationReleaseConfig(state, "app-foo", nil); err == nil || err.Error() != "APPLICATION_VOLUME_SOURCE_CONFLICT" {
		t.Fatalf("release preview got %v", err)
	}
}

func TestEnsureNodeVolumesOnlyReusesProvenLegacyVolumeOnTaskNode(t *testing.T) {
	ctx := context.Background()
	service := domain.Object{"Spec": domain.Object{"TaskTemplate": domain.Object{"ContainerSpec": domain.Object{"Mounts": []domain.Object{{"Type": "volume", "Source": "iquops-app-store", "Target": "/data"}}}, "Placement": domain.Object{"Constraints": []string{"node.id==node-a"}}}}}
	tests := []struct {
		name, volumes, inspect, expected string
		existing                         domain.Object
	}{
		{"adopt exact old mount", "iquops-app-store", `{"Name":"iquops-app-store","Driver":"local","Scope":"local","Options":{},"Labels":{}}`, "", service},
		{"reject other application", "iquops-app-store", `{"Name":"iquops-app-store","Driver":"local","Scope":"local","Options":{},"Labels":{"iquops.owner":"iquops","iquops.application-id":"other"}}`, "APPLICATION_VOLUME_OWNERSHIP_MISMATCH", service},
		{"reject plugin driver", "iquops-app-store", `{"Name":"iquops-app-store","Driver":"nfs","Scope":"global","Options":{"share":"remote"},"Labels":{}}`, "APPLICATION_VOLUME_OWNERSHIP_MISMATCH", service},
		{"reject missing legacy volume", "", "", "APPLICATION_VOLUME_MISSING", service},
		{"reject unrelated legacy volume", "iquops-app-store", `{"Name":"iquops-app-store","Driver":"local","Scope":"local","Options":{},"Labels":{}}`, "APPLICATION_VOLUME_OWNERSHIP_MISMATCH", domain.Object{"Spec": domain.Object{"TaskTemplate": domain.Object{"ContainerSpec": domain.Object{"Mounts": []domain.Object{{"Type": "volume", "Source": "iquops-app-other", "Target": "/other"}}}, "Placement": domain.Object{"Constraints": []string{"node.id==node-a"}}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := []string{}
			call := func(_ context.Context, command string, _ []byte, code string) (string, error) {
				commands = append(commands, command)
				switch {
				case strings.HasPrefix(command, "docker volume ls"):
					return test.volumes, nil
				case strings.HasPrefix(command, "docker volume inspect"):
					if test.inspect == "" {
						return "", errors.New(code)
					}
					return test.inspect, nil
				case strings.HasPrefix(command, "docker volume create"):
					return "iquops-app-store", nil
				default:
					return "", errors.New("unexpected query")
				}
			}
			err := ensureNodeVolumes(ctx, call, "app", domain.Object{"volumes": []domain.Object{{"name": "store", "target": "/data"}}}, test.existing)
			if test.expected != "" && (err == nil || err.Error() != test.expected) {
				t.Fatalf("got %v, want %s", err, test.expected)
			}
			if test.expected == "" && err != nil {
				t.Fatal(err)
			}
			if test.name == "adopt exact old mount" && len(commands) > 2 {
				t.Fatal("legacy volume must be reused")
			}
		})
	}
}

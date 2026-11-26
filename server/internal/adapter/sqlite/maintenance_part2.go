package sqlite

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func rebuildRetainedResourceIndex(ctx context.Context, conn *sql.Conn, state domain.State) error {
	resources := map[string]retainedResource{}
	links := map[string]retainedResourceLink{}
	addResource := func(resource retainedResource) {
		resource.Kind = strings.TrimSpace(resource.Kind)
		resource.ID = strings.TrimSpace(resource.ID)
		if resource.Kind == "" || resource.ID == "" {
			return
		}
		key := resource.Kind + "\x00" + resource.ID
		if existing, ok := resources[key]; ok {
			if existing.Name == "" && resource.Name != "" {
				existing.Name = resource.Name
			}
			if existing.Scope == "" && resource.Scope != "" {
				existing.Scope = resource.Scope
			}
			resources[key] = existing
			return
		}
		resources[key] = resource
	}
	addLink := func(sourceKind string, sourceID string, targetKind string, targetID string, relation string) {
		sourceID = strings.TrimSpace(sourceID)
		targetID = strings.TrimSpace(targetID)
		if sourceKind == "" || sourceID == "" || targetKind == "" || targetID == "" || relation == "" {
			return
		}
		addResource(retainedResource{Kind: sourceKind, ID: sourceID})
		addResource(retainedResource{Kind: targetKind, ID: targetID})
		link := retainedResourceLink{SourceKind: sourceKind, SourceID: sourceID, TargetKind: targetKind, TargetID: targetID, Relation: relation}
		links[sourceKind+"\x00"+sourceID+"\x00"+targetKind+"\x00"+targetID+"\x00"+relation] = link
	}
	addObjects := func(kind string, items []domain.Object) {
		for _, item := range items {
			addResource(retainedResource{
				Kind:  kind,
				ID:    domain.StringValue(item["id"]),
				Name:  firstMaintenanceString(item, "name", "displayName", "slug", "key", "id"),
				Scope: firstMaintenanceString(item, "projectId", "applicationId", "serverId"),
			})
		}
	}

	addObjects("server", state.Servers)
	addObjects("server_role", state.ServerRoles)
	addObjects("secret_asset", state.SecretAssets)
	addObjects("domain", state.Domains)
	addObjects("certificate", state.Certificates)
	addObjects("resolver", state.Resolvers)
	addObjects("ingress", state.Ingresses)
	addObjects("monitoring_agent", state.MonitoringAgents)
	addObjects("project", state.Projects)
	addObjects("application", state.Applications)
	addObjects("application_instance", state.ApplicationInstances)
	addObjects("application_deployment_config", state.ApplicationDeploymentConfigs)
	addObjects("route", state.Routes)
	addObjects("release_plan", state.ReleasePlans)
	addObjects("runtime_log_stream", state.RuntimeLogStreams)
	for _, tunnel := range state.FrpTunnels {
		addResource(retainedResource{Kind: "frp_tunnel", ID: tunnel.ID, Name: tunnel.Name, Scope: tunnel.ServerID})
		addLink("frp_tunnel", tunnel.ID, "server", tunnel.ServerID, "runs_on")
	}

	secretByRef := map[string]string{}
	for _, asset := range state.SecretAssets {
		if ref := domain.StringValue(asset["secretRef"]); ref != "" {
			secretByRef[ref] = domain.StringValue(asset["id"])
		}
	}
	for _, server := range state.Servers {
		serverID := domain.StringValue(server["id"])
		for _, projectID := range maintenanceStringSlice(server["projectIds"]) {
			addLink("server", serverID, "project", projectID, "assigned_to")
		}
		for _, secretID := range maintenanceStringSlice(server["secretAssetIds"]) {
			addLink("server", serverID, "secret_asset", secretID, "uses_secret")
		}
	}
	for _, role := range state.ServerRoles {
		addLink("server_role", domain.StringValue(role["id"]), "server", domain.StringValue(role["serverId"]), "belongs_to")
	}
	for _, project := range state.Projects {
		projectID := domain.StringValue(project["id"])
		for _, serverID := range maintenanceStringSlice(project["serverIds"]) {
			addLink("project", projectID, "server", serverID, "uses_server")
		}
		for _, applicationID := range maintenanceStringSlice(project["applicationIds"]) {
			addLink("project", projectID, "application", applicationID, "contains")
		}
	}
	for _, application := range state.Applications {
		applicationID := domain.StringValue(application["id"])
		addLink("application", applicationID, "project", domain.StringValue(application["projectId"]), "belongs_to")
		addSecretReferenceLinks("application", applicationID, application, secretByRef, addLink)
	}
	for _, config := range state.ApplicationDeploymentConfigs {
		configID := domain.StringValue(config["id"])
		addLink("application_deployment_config", configID, "application", domain.StringValue(config["applicationId"]), "configures")
		addLink("application_deployment_config", configID, "server", domain.StringValue(config["gatewayServerId"]), "uses_gateway")
		addLink("application_deployment_config", configID, "server", domain.StringValue(config["deploymentManagerServerId"]), "uses_deployment_manager")
		if placement := maintenanceObject(config["placement"]); placement != nil {
			for _, serverID := range maintenanceStringSlice(placement["eligibleServerIds"]) {
				addLink("application_deployment_config", configID, "server", serverID, "targets")
			}
		}
		if swarmRuntime := maintenanceObject(config["swarmRuntime"]); swarmRuntime != nil {
			for _, service := range maintenanceObjectSlice(swarmRuntime["services"]) {
				addLink("application_deployment_config", configID, "server", domain.StringValue(service["serverId"]), "targets")
			}
		}
		addSecretReferenceLinks("application_deployment_config", configID, config, secretByRef, addLink)
	}
	for _, route := range state.Routes {
		routeID := domain.StringValue(route["id"])
		addLink("route", routeID, "project", domain.StringValue(route["projectId"]), "belongs_to")
		addLink("route", routeID, "application", domain.StringValue(route["applicationId"]), "routes_to")
		for _, domainID := range maintenanceObjectIDs(route["domains"]) {
			addLink("route", routeID, "domain", domainID, "uses_domain")
		}
		for _, domainID := range maintenanceStringSlice(route["domainIds"]) {
			addLink("route", routeID, "domain", domainID, "uses_domain")
		}
	}
	for _, item := range state.Domains {
		id := domain.StringValue(item["id"])
		addLink("domain", id, "certificate", domain.StringValue(item["certificateId"]), "uses_certificate")
		addLink("domain", id, "domain", domain.StringValue(item["parentDomainId"]), "child_of")
	}
	for _, item := range state.Certificates {
		id := domain.StringValue(item["id"])
		addLink("certificate", id, "resolver", domain.StringValue(item["resolverId"]), "uses_resolver")
		addSecretReferenceLinks("certificate", id, item, secretByRef, addLink)
	}
	for _, item := range state.Resolvers {
		addLink("resolver", domain.StringValue(item["id"]), "ingress", domain.StringValue(item["ingressId"]), "uses_ingress")
	}
	for _, item := range state.Ingresses {
		id := domain.StringValue(item["id"])
		for _, serverID := range maintenanceStringSlice(item["targetServers"]) {
			addLink("ingress", id, "server", serverID, "targets")
		}
	}
	for _, item := range state.ApplicationInstances {
		id := domain.StringValue(item["id"])
		addLink("application_instance", id, "project", domain.StringValue(item["projectId"]), "belongs_to")
		addLink("application_instance", id, "application", domain.StringValue(item["applicationId"]), "instance_of")
		addLink("application_instance", id, "server", domain.StringValue(item["serverId"]), "runs_on")
		addLink("application_instance", id, "release_plan", domain.StringValue(item["releasePlanId"]), "created_by")
	}
	for _, item := range state.ReleasePlans {
		id := domain.StringValue(item["id"])
		addLink("release_plan", id, "project", domain.StringValue(item["projectId"]), "belongs_to")
		addLink("release_plan", id, "application", domain.StringValue(item["applicationId"]), "releases")
		addLink("release_plan", id, "server", domain.StringValue(item["deploymentManagerServerId"]), "uses_deployment_manager")
	}
	for _, item := range state.MonitoringAgents {
		addLink("monitoring_agent", domain.StringValue(item["id"]), "server", domain.StringValue(item["serverId"]), "monitors")
	}

	resourceKeys := make([]string, 0, len(resources))
	for key := range resources {
		resourceKeys = append(resourceKeys, key)
	}
	sort.Strings(resourceKeys)
	for _, key := range resourceKeys {
		resource := resources[key]
		if _, err := conn.ExecContext(ctx, `INSERT INTO resource_inventory
			(resource_kind, resource_id, resource_name, resource_scope, labels, updated_at)
			VALUES (?, ?, ?, ?, '{}', ?)`, resource.Kind, resource.ID, resource.Name, resource.Scope, domain.Now()); err != nil {
			return err
		}
	}
	linkKeys := make([]string, 0, len(links))
	for key := range links {
		linkKeys = append(linkKeys, key)
	}
	sort.Strings(linkKeys)
	for _, key := range linkKeys {
		link := links[key]
		if _, err := conn.ExecContext(ctx, `INSERT INTO resource_links
			(source_kind, source_id, target_kind, target_id, relation, metadata, updated_at)
			VALUES (?, ?, ?, ?, ?, '{}', ?)`, link.SourceKind, link.SourceID, link.TargetKind, link.TargetID, link.Relation, domain.Now()); err != nil {
			return err
		}
	}
	return nil
}

func firstMaintenanceString(item domain.Object, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(domain.StringValue(item[key])); value != "" {
			return value
		}
	}
	return ""
}

func maintenanceStringSlice(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []string:
		for _, item := range typed {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
	case []any:
		for _, item := range typed {
			if text := strings.TrimSpace(domain.StringValue(item)); text != "" {
				result = append(result, text)
			}
		}
	}
	return result
}

func maintenanceObject(value any) domain.Object {
	switch typed := value.(type) {
	case domain.Object:
		return typed
	case map[string]any:
		return domain.Object(typed)
	default:
		return nil
	}
}

func maintenanceObjectSlice(value any) []domain.Object {
	switch typed := value.(type) {
	case []domain.Object:
		return typed
	case []map[string]any:
		result := make([]domain.Object, 0, len(typed))
		for _, item := range typed {
			result = append(result, domain.Object(item))
		}
		return result
	case []any:
		result := []domain.Object{}
		for _, item := range typed {
			if object := maintenanceObject(item); object != nil {
				result = append(result, object)
			}
		}
		return result
	default:
		return nil
	}
}

func maintenanceObjectIDs(value any) []string {
	result := []string{}
	for _, item := range maintenanceObjectSlice(value) {
		if id := domain.StringValue(item["id"]); id != "" {
			result = append(result, id)
		}
	}
	return result
}

func addSecretReferenceLinks(sourceKind string, sourceID string, value any, secretByRef map[string]string, addLink func(string, string, string, string, string)) {
	walkSecretReferences(value, func(secretRef string) {
		if secretID := secretByRef[secretRef]; secretID != "" {
			addLink(sourceKind, sourceID, "secret_asset", secretID, "uses_secret")
		}
	})
}

func walkSecretReferences(value any, visit func(string)) {
	switch typed := value.(type) {
	case domain.Object:
		walkSecretReferenceMap(map[string]any(typed), visit)
	case map[string]any:
		walkSecretReferenceMap(typed, visit)
	case []domain.Object:
		for _, item := range typed {
			walkSecretReferences(item, visit)
		}
	case []map[string]any:
		for _, item := range typed {
			walkSecretReferences(item, visit)
		}
	case []any:
		for _, item := range typed {
			walkSecretReferences(item, visit)
		}
	}
}

func walkSecretReferenceMap(value map[string]any, visit func(string)) {
	for key, nested := range value {
		if strings.HasSuffix(strings.ToLower(key), "secretref") {
			if secretRef := strings.TrimSpace(domain.StringValue(nested)); secretRef != "" {
				visit(secretRef)
			}
		}
		walkSecretReferences(nested, visit)
	}
}

package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type RouteRuntimeExecutor interface {
	Apply(context.Context, RouteRuntimeInput) (domain.Object, error)
	Remove(context.Context, RouteRuntimeInput) (domain.Object, error)
}

type RouteRuntimeInput struct {
	CertResolver       string
	LegacyRedirectHost string
	Redirect           domain.Object
	Route              domain.Object
	ApplicationID      string
	Gateway            domain.Object
	SSHKey             string
	Domains            []domain.Object
	Services           []domain.Object
	Rules              []domain.Object
	Network            string
	ClusterID          string
	Revision           string
	HTTPPort           int
	HTTPSPort          int
	HTTPSOnly          bool
	TraefikName        string
	PossibleSnapshots  []domain.Object
	Log                func(string, string, string, string)
}

func (r *Runtime) routeRuntimeInput(ctx context.Context, state domain.State, route domain.Object, removing bool, task domain.Task) (RouteRuntimeInput, error) {
	applicationID := stringValue(route["applicationId"])
	application := findObject(state.Applications, applicationID)
	if application == nil {
		return RouteRuntimeInput{}, errors.New("APPLICATION_NOT_FOUND")
	}
	config := objectMapFromAny(task.Payload["runtimeConfigSnapshot"])
	if len(config) == 0 {
		config = findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
	}
	gatewayID := stringValue(config["gatewayServerId"])
	if !removing && gatewayID == "" {
		return RouteRuntimeInput{}, errors.New("APPLICATION_GATEWAY_SERVER_REQUIRED")
	}
	snapshot := objectMapFromAny(route["lastApplied"])
	if pending := objectMapFromAny(route["pendingRemote"]); len(pending) > 0 {
		snapshot = pending
	}
	domains, rules := []domain.Object{}, objectSlice(route["rules"])
	services := []domain.Object{}
	if removing {
		gatewayID = stringValue(snapshot["gatewayServerId"])
		domains, rules = objectSlice(snapshot["domains"]), objectSlice(snapshot["rules"])
	} else {
		for _, bound := range objectSlice(route["domains"]) {
			asset := findObject(state.Domains, stringValue(bound["id"]))
			if asset == nil {
				return RouteRuntimeInput{}, errors.New("ROUTE_DOMAIN_NOT_FOUND")
			}
			domainSnapshot, err := routeDomainSnapshot(state, asset, gatewayID)
			if err != nil {
				return RouteRuntimeInput{}, err
			}
			if objectMapFromAny(asset["redirectRuntime"])["legacyUpgrade"] == true {
				domainSnapshot["inheritedUpgrade"] = true
			}
			domains = append(domains, domainSnapshot)
		}
		configured := objectSlice(objectMapFromAny(config["swarmRuntime"])["services"])
		positiveWeight := false
		for _, bound := range objectSlice(route["services"]) {
			service := findObjectBy(configured, "slotKey", stringValue(bound["slotKey"]))
			if service == nil || stringValue(bound["serviceName"]) != stringValue(service["serviceName"]) {
				return RouteRuntimeInput{}, errors.New("ROUTE_SERVICE_INVALID")
			}
			weight, err := validatedRouteServiceWeight(bound)
			if err != nil {
				return RouteRuntimeInput{}, err
			}
			positiveWeight = positiveWeight || weight > 0
			target := cloneObject(service)
			target["weight"] = int(weight)
			port, err := routeHTTPPort(objectMapFromAny(config["swarmRuntime"]), stringValue(bound["portName"]))
			if err != nil {
				return RouteRuntimeInput{}, err
			}
			if port != "" {
				target["portName"] = port
			}
			services = append(services, target)
		}
		if len(domains) == 0 || len(services) == 0 || len(rules) == 0 {
			return RouteRuntimeInput{}, errors.New("ROUTE_CONFIG_INVALID")
		}
		if !positiveWeight {
			return RouteRuntimeInput{}, errors.New("ROUTE_SERVICE_WEIGHT_REQUIRED")
		}
		if err := rejectConflictingRoute(state, route, domains, rules); err != nil {
			return RouteRuntimeInput{}, err
		}
	}
	gateway := findObject(state.Servers, gatewayID)
	if gateway == nil || !serverAvailableToProject(gateway, stringValue(application["projectId"])) {
		return RouteRuntimeInput{}, errors.New("APPLICATION_GATEWAY_SERVER_REQUIRED")
	}
	if !removing {
		if err := validateRouteDeploymentCluster(state, application, config); err != nil {
			return RouteRuntimeInput{}, err
		}
	}
	sshKey, ok := r.serverSSHSecretMaterial(ctx, state, gateway)
	if !ok {
		return RouteRuntimeInput{}, errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
	}
	input := RouteRuntimeInput{
		CertResolver: r.Config.TraefikCertResolver,

		Route: cloneObject(route), ApplicationID: applicationID, Gateway: cloneObject(gateway), SSHKey: sshKey,
		Domains: domains, Services: services, Rules: rules, Network: r.Config.SwarmNetwork,
		ClusterID: stringValue(serverRuntimeMetrics(gateway)["swarmClusterId"]), HTTPPort: r.Config.TraefikHTTPPort,
		HTTPSPort: r.Config.TraefikHTTPSPort, TraefikName: r.Config.TraefikService,
		Log: func(source, phase, level, message string) {
			r.appendRuntimeMessage(task, source, phase, level, message)
		},
	}
	metrics := serverRuntimeMetrics(gateway)
	input.Network = firstNonEmptyString(stringValue(objectMapFromAny(config["swarmRuntime"])["overlayNetwork"]), input.Network)
	if port := int(numberValue(metrics["traefikHttpPort"])); port > 0 {
		input.HTTPPort = port
	}
	if port := int(numberValue(metrics["traefikHttpsPort"])); port > 0 {
		input.HTTPSPort = port
	}
	input.Revision = routeRevision(domain.Object{"domains": domains, "services": services, "rules": rules, "gateway": gatewayID, "network": input.Network, "clusterId": input.ClusterID, "httpPort": input.HTTPPort, "httpsPort": input.HTTPSPort, "traefikName": input.TraefikName})
	if removing {
		input.PossibleSnapshots = possibleRouteSnapshots(route)
		input.Revision = stringValue(snapshot["revision"])
		input.Network = firstNonEmptyString(stringValue(snapshot["network"]), input.Network)
		input.ClusterID = firstNonEmptyString(stringValue(snapshot["clusterId"]), input.ClusterID)
		if port := int(numberValue(snapshot["httpPort"])); port > 0 {
			input.HTTPPort = port
		}
		if port := int(numberValue(snapshot["httpsPort"])); port > 0 {
			input.HTTPSPort = port
		}
	}
	return input, nil
}

func validateRouteDeploymentCluster(state domain.State, application, config domain.Object) error {
	manager := findObject(state.Servers, stringValue(config["deploymentManagerServerId"]))
	if manager == nil {
		return errors.New("APPLICATION_DEPLOYMENT_MANAGER_REQUIRED")
	}
	gateway := findObject(state.Servers, stringValue(config["gatewayServerId"]))
	managerMetrics, gatewayMetrics := serverRuntimeMetrics(manager), serverRuntimeMetrics(gateway)
	if swarmIdentityReadinessCode(managerMetrics) != "" || swarmIdentityReadinessCode(gatewayMetrics) != "" {
		return errors.New("SWARM_CLUSTER_UNVERIFIED")
	}
	cluster := stringValue(managerMetrics["swarmClusterId"])
	if cluster == "" || cluster != stringValue(gatewayMetrics["swarmClusterId"]) {
		return errors.New("APPLICATION_SWARM_CLUSTER_MISMATCH")
	}
	return validateDeploymentLocation(application, config, cluster, false)
}

func boundRouteDomain(asset domain.Object) domain.Object {
	result := cloneObject(asset)
	delete(result, "redirect")
	delete(result, "redirectRuntime")
	return result
}

func routeDomainSnapshot(state domain.State, asset domain.Object, gatewayID string) (domain.Object, error) {
	if !validDomainName(stringValue(asset["name"])) {
		return nil, errors.New("ROUTE_DOMAIN_INVALID")
	}
	result := boundRouteDomain(asset)
	certificateID := stringValue(asset["certificateId"])
	result["tls"] = certificateID != ""
	if certificateID == "" {
		return result, nil
	}
	certificate := findObject(state.Certificates, certificateID)
	if certificate == nil || stringValue(certificate["issuer"]) != "lets_encrypt" || stringValue(certificate["challengeType"]) != "http-01" || !containsString(objectStringSlice(certificate["domains"]), stringValue(asset["name"])) {
		return nil, errors.New("ROUTE_CERTIFICATE_INVALID")
	}
	resolver := findObject(state.Resolvers, stringValue(certificate["resolverId"]))
	ingress := findObject(state.Ingresses, stringValue(resolver["ingressId"]))
	if resolver == nil || ingress == nil || !containsString(objectStringSlice(ingress["serverIds"]), gatewayID) {
		return nil, errors.New("ROUTE_CERTIFICATE_GATEWAY_MISMATCH")
	}
	return result, nil
}

func rejectConflictingRoute(state domain.State, route domain.Object, domains, rules []domain.Object) error {
	for _, bound := range domains {
		scheme := "http"
		if bound["tls"] == true {
			scheme = "https"
		}
		if jenkinsDomainInUse(state, stringValue(bound["id"]), "") || redirectBlocksBackend(findObject(state.Domains, stringValue(bound["id"])), scheme) {
			return errors.New("DOMAIN_REDIRECT_HOST_IN_USE")
		}
	}
	for _, other := range state.Routes {
		if stringValue(other["id"]) == stringValue(route["id"]) {
			continue
		}
		for _, snapshot := range possibleRouteSnapshots(other) {
			for _, left := range domains {
				for _, right := range objectSlice(snapshot["domains"]) {
					if stringValue(left["name"]) != stringValue(right["name"]) {
						continue
					}
					for _, rule := range rules {
						for _, existing := range objectSlice(snapshot["rules"]) {
							if stringValue(rule["path"]) == stringValue(existing["path"]) {
								return errors.New("ROUTE_HOST_PATH_CONFLICT")
							}
						}
					}
				}
			}
		}
	}
	return nil
}

func possibleRouteSnapshots(route domain.Object) []domain.Object {
	result := []domain.Object{}
	for _, key := range []string{"lastApplied", "pendingRemote"} {
		if snapshot := objectMapFromAny(route[key]); len(snapshot) > 0 {
			result = append(result, snapshot)
		}
	}
	return result
}

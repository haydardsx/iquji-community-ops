package application

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

var templateIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,79}$`)

func (r *Runtime) ApplicationTemplates(ctx context.Context) (domain.Object, error) {
	state, err := r.Store.LoadState(ctx)
	return domain.Object{"version": 1, "items": state.ApplicationTemplates}, err
}

func validateApplicationTemplate(item domain.Object) error {
	id, _ := item["id"].(string)
	name, _ := item["name"].(string)
	description, described := item["description"].(string)
	if !objectOnlyContainsKeys(item, "id", "name", "description", "config") || !templateIDPattern.MatchString(id) || id == "import" || strings.TrimSpace(name) == "" || len(name) > 240 || !described || len(description) > 4000 {
		return errors.New("APPLICATION_TEMPLATE_INVALID")
	}
	cfg := objectMapFromAny(item["config"])
	if !objectOnlyContainsKeys(cfg, "sourceConfig", "swarmRuntime", "environmentVariables", "outputVariables") {
		return errors.New("APPLICATION_TEMPLATE_NOT_PORTABLE")
	}
	source := objectMapFromAny(cfg["sourceConfig"])
	if _, bound := source["authSecretRef"]; bound {
		return errors.New("APPLICATION_TEMPLATE_NOT_PORTABLE")
	}
	for _, service := range objectSlice(objectMapFromAny(cfg["swarmRuntime"])["services"]) {
		if !objectOnlyContainsKeys(service, "slotKey", "replicas") {
			return errors.New("APPLICATION_TEMPLATE_NOT_PORTABLE")
		}
	}
	for _, field := range []string{"environmentVariables", "outputVariables"} {
		for _, variable := range objectSlice(cfg[field]) {
			for _, key := range []string{"id", "secretRef", "outputId", "outputApplicationId", "routeId", "maskedSummary"} {
				if _, bound := variable[key]; bound {
					return errors.New("APPLICATION_TEMPLATE_NOT_PORTABLE")
				}
			}
			if _, plain := variable["plainValue"]; plain && variable["valueSource"] != "plain_text" {
				return errors.New("APPLICATION_TEMPLATE_NOT_PORTABLE")
			}
		}
	}
	return createTemplateDraft(&domain.State{}, domain.Object{"id": "template-check"}, cfg)
}

func (r *Runtime) SaveApplicationTemplate(ctx context.Context, id string, item domain.Object, create bool) error {
	if id != item["id"] {
		return errors.New("APPLICATION_TEMPLATE_INVALID")
	}
	if err := validateApplicationTemplate(item); err != nil {
		return err
	}
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		index := slices.IndexFunc(state.ApplicationTemplates, func(t domain.Object) bool { return t["id"] == id })
		if create {
			if index >= 0 {
				return errors.New("APPLICATION_TEMPLATE_CONFLICT")
			}
			state.ApplicationTemplates = append(state.ApplicationTemplates, item)
		} else {
			if index < 0 {
				return errors.New("APPLICATION_TEMPLATE_NOT_FOUND")
			}
			state.ApplicationTemplates[index] = item
		}
		return nil
	})
}

func (r *Runtime) DeleteApplicationTemplate(ctx context.Context, id string) error {
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		index := slices.IndexFunc(state.ApplicationTemplates, func(t domain.Object) bool { return t["id"] == id })
		if index < 0 {
			return errors.New("APPLICATION_TEMPLATE_NOT_FOUND")
		}
		state.ApplicationTemplates = slices.Delete(state.ApplicationTemplates, index, index+1)
		return nil
	})
}

func instantiateApplicationTemplate(state *domain.State, app domain.Object, id string) error {
	if item := findObject(state.ApplicationTemplates, id); item != nil {
		return createTemplateDraft(state, app, objectMapFromAny(item["config"]))
	}
	return errors.New("APPLICATION_TEMPLATE_NOT_FOUND")
}

func createTemplateDraft(state *domain.State, app, cfg domain.Object) error {
	cfg = domain.CloneObjectWithoutUserReferences(cfg)
	if len(objectMapFromAny(cfg["sourceConfig"])) == 0 {
		return errors.New("APPLICATION_SOURCE_PROVIDER_INVALID")
	}
	if err := validateManagedDeploymentConfigInput(cfg); err != nil {
		return err
	}
	appID := stringValue(app["id"])
	swarm := objectMapFromAny(cfg["swarmRuntime"])
	if len(swarm) == 0 {
		return errors.New("APPLICATION_SWARM_CONFIG_INVALID")
	}
	if raw, ok := swarm["services"]; ok && !applicationObjectArray(raw) {
		return errors.New("APPLICATION_SERVICE_CONFIG_INVALID")
	}
	services := objectSlice(swarm["services"])
	if len(services) == 0 {
		services = []domain.Object{{"slotKey": "main", "replicas": 1}}
	}
	for _, service := range services {
		name := safeName(appID)
		if slot := stringValue(service["slotKey"]); slot != "main" {
			name += "-" + safeName(slot)
		}
		service["serviceName"] = name
	}
	swarm["services"] = services
	for _, v := range objectSlice(cfg["outputVariables"]) {
		v["id"] = domain.NewID("out")
	}
	for _, v := range objectSlice(cfg["environmentVariables"]) {
		v["id"] = domain.NewID("var")
	}
	if err := normalizeApplicationConfiguration(*state, app, cfg, true); err != nil {
		return err
	}
	cfg["id"], cfg["applicationId"], cfg["createdAt"], cfg["updatedAt"] = domain.NewID("depconf"), appID, app["createdAt"], app["updatedAt"]
	state.ApplicationDeploymentConfigs = append(state.ApplicationDeploymentConfigs, cfg)
	updateApplicationSourceKind(app, cfg)
	return nil
}

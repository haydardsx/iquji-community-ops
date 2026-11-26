package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func applicationReleaseConfig(state domain.State, appID string, request domain.Object) (domain.Object, error) {
	if findObject(state.Applications, appID) == nil {
		return nil, errors.New("APPLICATION_NOT_FOUND")
	}
	cfg := cloneObject(findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", appID))
	if cfg == nil {
		return nil, errors.New("APPLICATION_DEPLOYMENT_CONFIG_REQUIRED")
	}
	source := cloneObject(objectMapFromAny(cfg["sourceConfig"]))
	if source == nil {
		return nil, errors.New("APPLICATION_SOURCE_CONFIG_INVALID")
	}
	for _, key := range []string{"ref", "refType"} {
		if value := stringValue(request[key]); value != "" {
			source[key] = value
		}
	}
	source, err := validateApplicationSourceConfig(source)
	if err != nil {
		return nil, err
	}
	cfg["sourceConfig"] = source
	if sync := registrySyncConfig(cfg); sync != nil && source["provider"] == "git" {
		if err := validateRegistrySync(sync); err != nil {
			return nil, err
		}
		if err := validateRegistryCredential(state, sync); err != nil {
			return nil, err
		}
	}
	swarm := objectMapFromAny(cfg["swarmRuntime"])
	if err := validateContainerRuntime(swarm); err != nil {
		return nil, err
	}
	return cfg, validateVolumeClaims(state, appID, swarm)
}

func applicationReleaseReadiness(state domain.State, config domain.Object) domain.CheckReport {
	appID := stringValue(config["applicationId"])
	app := findObject(state.Applications, appID)
	subject := resourceRef("application", appID, firstNonEmptyString(stringValue(app["name"]), appID))
	builder := domain.NewCheckReportBuilder(domain.CheckKindPreflight, subject)
	addRuntimeReadinessChecks(builder, deploymentRuntimeReadiness(state, config), "release-plan-preview")
	builder.Impact(domain.ResourceActionCreate, resourceRef("release_plan", "release_plan_preview", "发布计划"), 1, domain.CheckSeverityInfo, "CREATE_RELEASE_PLAN")
	return builder.Report()
}

func (r *Runtime) ReleasePlanPreview(ctx context.Context, appID string, input domain.Object) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	config, err := applicationReleaseConfig(state, appID, input)
	if err != nil {
		return nil, err
	}
	if _, err := applicationInputSnapshot(state, appID, config); err != nil {
		return nil, err
	}
	source := objectMapFromAny(config["sourceConfig"])
	report := applicationReleaseReadiness(state, config)
	preview := domain.Object{"applicationId": appID, "provider": source["provider"], "preflightSummary": domain.Object{"runtimeReady": report.Decision.Allowed, "warnings": report.Decision.Warnings}}
	if source["provider"] == "git" {
		preview["ref"], preview["refType"], preview["buildServerId"] = source["ref"], source["refType"], config["buildServerId"]
	}
	return processResponse(report, "releasePlan", preview), nil
}

// 预检、变量解析与快照写入共用创建发布计划的事务。
func newApplicationReleasePlan(state *domain.State, appID string, request domain.Object) (domain.Object, error) {
	config, err := applicationReleaseConfig(*state, appID, request)
	if err != nil {
		return nil, err
	}
	if report := applicationReleaseReadiness(*state, config); !report.Decision.Allowed {
		return nil, newReleasePlanPreviewBlockedError(report)
	}
	inputs, err := applicationInputSnapshot(*state, appID, config)
	if err != nil {
		return nil, err
	}
	app := findObject(state.Applications, appID)
	now := domain.Now()
	plan := domain.Object{
		"id": domain.NewID("rel"), "projectId": app["projectId"], "applicationId": appID,
		"status": "queued", "createdAt": now, "updatedAt": now, "deploymentManagerServerId": config["deploymentManagerServerId"],
		"deploymentClusterId": stringValue(serverRuntimeMetrics(findObject(state.Servers, stringValue(config["deploymentManagerServerId"])))["swarmClusterId"]),
		"buildSnapshot":       releasePlanBuildSnapshot(app, config), "configSnapshot": inputs,
		"buildOutputKeys": applicationBuildOutputKeys(config),
	}
	source := objectMapFromAny(config["sourceConfig"])
	if !isContainerSource(source) {
		plan["buildServerId"] = config["buildServerId"]
	}
	if !isContainerSource(source) || source["provider"] == "image" {
		mirrors, err := snapshotNodePullMirrors(*state, config)
		if err != nil {
			return nil, err
		}
		objectMapFromAny(plan["buildSnapshot"])["nodePullMirrors"] = mirrors
	}
	if sync := registrySyncConfig(config); sync["enabled"] == true && objectMapFromAny(config["sourceConfig"])["provider"] == "git" {
		snapshot := objectMapFromAny(plan["buildSnapshot"])
		platforms := domain.Object{}
		for _, service := range objectSlice(objectMapFromAny(snapshot["swarmRuntime"])["services"]) {
			platforms[stringValue(service["slotKey"])] = dockerTargetPlatform(findObject(state.Servers, stringValue(service["serverId"])))
		}
		snapshot["slotPlatforms"] = platforms
		if asset := credentialForPurpose(*state, stringValue(sync["registrySecretRef"]), "registry_secret"); asset != nil {
			plan["registryRevisionSecretRef"] = asset["materialRef"]
		}
	}
	state.ReleasePlans = append(state.ReleasePlans, plan)
	return plan, nil
}

func (r *Runtime) CreateReleasePlan(ctx context.Context, appID string, request domain.Object) (domain.Object, error) {
	var result domain.Object
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		plan, err := newApplicationReleasePlan(state, appID, request)
		if err != nil {
			return err
		}
		state.OperationLogs = append(state.OperationLogs, newOperationLog("create_release_plans", "release_plans", stringValue(plan["id"]), "succeeded", "已创建应用发布计划", ""))
		result = publicApplicationBuild(plan, nil)
		return nil
	})
	return result, err
}

func (r *Runtime) CreateApplicationReleaseOperation(ctx context.Context, appID, planID string) (domain.Task, error) {
	var task domain.Task
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		if reused, err := reuseTaskRequest(ctx, *state, &task); reused || err != nil {
			return err
		}
		var plan domain.Object
		if planID == "" {
			var err error
			plan, err = newApplicationReleasePlan(state, appID, nil)
			if err != nil {
				return err
			}
		} else {
			plan = findObject(state.ReleasePlans, planID)
			if plan == nil {
				return errors.New("RELEASE_PLAN_NOT_FOUND")
			}
			if plan["status"] != "queued" || stringValue(plan["operationRunId"]) != "" {
				return errors.New("RELEASE_PLAN_STATE_INVALID")
			}
			appID = stringValue(plan["applicationId"])
			config := releasePlanRuntimeConfig(plan)
			if len(objectMapFromAny(config["sourceConfig"])) == 0 {
				return errors.New("APPLICATION_RELEASE_SNAPSHOT_REQUIRED")
			}
			swarm := objectMapFromAny(config["swarmRuntime"])
			if err := validateContainerRuntime(swarm); err != nil {
				return err
			}
			if err := validateVolumeClaims(*state, appID, swarm); err != nil {
				return err
			}
			if report := applicationReleaseReadiness(*state, config); !report.Decision.Allowed {
				return newReleasePlanPreviewBlockedError(report)
			}
		}
		app := findObject(state.Applications, appID)
		if app == nil {
			return errors.New("APPLICATION_NOT_FOUND")
		}
		action := domain.TaskActionRequestJenkinsRelease
		if isContainerSource(objectMapFromAny(objectMapFromAny(plan["buildSnapshot"])["sourceSnapshot"])) {
			action = domain.TaskActionDeployApplication
		}
		task = newTask(string(action), "application", appID, stringValue(app["name"]), map[string]any{"applicationId": appID, "projectId": app["projectId"], "releasePlanId": plan["id"]})
		plan["operationRunId"] = task.ID
		return enqueueTaskRequest(ctx, state, &task)
	})
	return task, err
}

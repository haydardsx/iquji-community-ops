package application

import (
	"context"
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const jenkinsReleaseExecutionTimeout = 2 * time.Hour

func (r *Runtime) RefreshReleasePlanRuntimeReadiness(ctx context.Context, releasePlanID string) (domain.CheckReport, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.CheckReport{}, err
	}
	releasePlan := findObject(state.ReleasePlans, releasePlanID)
	if releasePlan == nil {
		return domain.CheckReport{}, errors.New("RELEASE_PLAN_NOT_FOUND")
	}
	config := releasePlanRuntimeConfig(releasePlan)
	probeFailures := map[string]bool{}
	for _, serverID := range releaseRuntimeServerIDs(config) {
		if _, err := r.ServerRuntimeProbe(ctx, serverID); err != nil {
			probeFailures[serverID] = true
		}
	}

	state, err = r.Store.LoadState(ctx)
	if err != nil {
		return domain.CheckReport{}, err
	}
	for id, value := range objectMapFromAny(objectMapFromAny(releasePlan["buildSnapshot"])["nodePullMirrors"]) {
		frozen := objectMapFromAny(value)
		if frozen["origin"] != "daemon" || frozen["verifyDaemon"] == true || probeFailures[id] {
			continue
		}
		metrics := serverRuntimeMetrics(findObject(state.Servers, id))
		if metrics["dockerRegistryMirrorReady"] == false {
			probeFailures[id] = true
			continue
		}
		if actual, probed := metrics["dockerRegistryMirrors"]; probed {
			if err := r.pinFirstDaemonMirrors(ctx, releasePlanID, id, objectStringSlice(actual)); err != nil {
				return domain.CheckReport{}, err
			}
		}
	}
	subject := resourceRef(
		"release_plan",
		releasePlanID,
		firstNonEmptyString(stringValue(releasePlan["name"]), releasePlanID),
	)
	builder := domain.NewCheckReportBuilder(domain.CheckKindPreflight, subject)
	for serverID := range probeFailures {
		server := findObject(state.Servers, serverID)
		builder.Check(
			"RUNTIME_PROBE_FAILED",
			"runtime",
			"release-execution-preflight",
			resourceRef("server", serverID, firstNonEmptyString(stringValue(server["name"]), serverID)),
			domain.CheckOutcomeFailed,
			domain.CheckSeverityDanger,
			true,
			"required server runtime could not be refreshed",
			domain.Object{"serverId": serverID},
		)
	}
	addRuntimeReadinessChecks(builder, deploymentRuntimeReadiness(state, config), "release-execution-preflight")
	report := builder.Report()
	return report, nil
}

func releasePlanRuntimeConfig(plan domain.Object) domain.Object {
	build := objectMapFromAny(plan["buildSnapshot"])
	return domain.Object{
		"applicationId":             plan["applicationId"],
		"sourceConfig":              build["sourceSnapshot"],
		"deliveryConfig":            build["deliveryConfig"],
		"buildServerId":             firstNonEmptyString(stringValue(plan["buildServerId"]), stringValue(build["buildServerId"])),
		"deploymentManagerServerId": plan["deploymentManagerServerId"],
		"deploymentClusterId":       plan["deploymentClusterId"],
		"swarmRuntime":              build["swarmRuntime"],
	}
}

func releaseRuntimeServerIDs(config domain.Object) []string {
	seen := map[string]bool{}
	for _, serverID := range []string{
		stringValue(config["buildServerId"]),
		stringValue(config["deploymentManagerServerId"]),
	} {
		if serverID != "" {
			seen[serverID] = true
		}
	}
	for _, service := range objectSlice(objectMapFromAny(config["swarmRuntime"])["services"]) {
		if serverID := stringValue(service["serverId"]); serverID != "" {
			seen[serverID] = true
		}
	}
	result := make([]string, 0, len(seen))
	for serverID := range seen {
		result = append(result, serverID)
	}
	sort.Strings(result)
	return result
}

func (r *Runtime) executeRequestJenkinsRelease(ctx context.Context, task domain.Task) (string, error) {
	return r.executeManagedJenkinsRelease(ctx, task)
}

func (r *Runtime) resolveJenkinsReleasePlanForTask(ctx context.Context, task domain.Task) (string, error) {
	if strings.TrimSpace(task.TargetID) == "" {
		return "", errors.New("JENKINS_RELEASE_PLAN_REQUIRED")
	}
	if task.TargetKind == "release_plan" {
		return task.TargetID, nil
	}
	if task.TargetKind != "application" {
		return "", errors.New("JENKINS_RELEASE_PLAN_REQUIRED")
	}
	var id string
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		if plan := findObjectBy(state.ReleasePlans, "operationRunId", task.ID); plan != nil {
			if stringValue(plan["applicationId"]) != task.TargetID {
				return errors.New("JENKINS_RELEASE_PLAN_REQUIRED")
			}
			id = stringValue(plan["id"])
			return nil
		}
		if stringValue(task.Payload["releasePlanId"]) != "" {
			return errors.New("RELEASE_PLAN_NOT_FOUND")
		}
		// 旧队列任务可能只保存应用标识。首次领取时在同一个事务中补齐发布快照与绑定。
		plan, err := newApplicationReleasePlan(state, task.TargetID, task.Payload)
		if err != nil {
			return err
		}
		plan["operationRunId"] = task.ID
		id = stringValue(plan["id"])
		for i := range state.Tasks {
			if state.Tasks[i].ID == task.ID {
				if state.Tasks[i].Payload == nil {
					state.Tasks[i].Payload = map[string]any{}
				}
				state.Tasks[i].Payload["releasePlanId"] = id
				break
			}
		}
		return nil
	})
	return id, err
}

func localControlPlaneAddress(host string, port int) string {
	host = strings.TrimSpace(host)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	case "localhost":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func (r *Runtime) createJenkinsReleaseRun(
	ctx context.Context,
	task domain.Task,
	releasePlan domain.Object,
	state domain.State,
	runID string,
	runToken string,
	controlPlaneBaseURL string,
) (domain.Object, error) {
	materialsSnapshot, _, err := jenkinsReleaseMaterials(state, releasePlan, runID, controlPlaneBaseURL)
	if err != nil {
		return nil, err
	}
	now := domain.Now()
	run := domain.Object{
		"id":                  runID,
		"releasePlanId":       stringValue(releasePlan["id"]),
		"applicationId":       stringValue(releasePlan["applicationId"]),
		"operationRunId":      task.ID,
		"tokenHash":           domain.HashSecret(runToken),
		"callbackTokenHash":   domain.HashSecret(jenkinsPurposeToken(runToken, "callback")),
		"status":              "pending",
		"controlPlaneBaseUrl": controlPlaneBaseURL,
		"materialsSnapshot":   materialsSnapshot,
		"createdAt":           now,
		"expiresAt":           time.Now().UTC().Add(jenkinsReleaseExecutionTimeout).Format(time.RFC3339Nano),
		"updatedAt":           now,
	}
	err = r.Store.MutateState(ctx, func(next *domain.State) error {
		if findObjectBy(next.JenkinsReleaseRuns, "releasePlanId", stringValue(releasePlan["id"])) != nil {
			return errors.New("JENKINS_RUN_ALREADY_REGISTERED")
		}
		if err := pinReleaseLocation(next, releasePlan); err != nil {
			return err
		}
		run["attempt"] = 1
		next.JenkinsReleaseRuns = append(next.JenkinsReleaseRuns, cloneObject(run))
		for index := range next.ReleasePlans {
			if stringValue(next.ReleasePlans[index]["id"]) != stringValue(releasePlan["id"]) {
				continue
			}
			next.ReleasePlans[index]["status"] = "executing"
			next.ReleasePlans[index]["operationRunId"] = task.ID
			next.ReleasePlans[index]["jenkinsRunId"] = runID
			next.ReleasePlans[index]["updatedAt"] = now
			return nil
		}
		return errors.New("RELEASE_PLAN_NOT_FOUND")
	})
	return run, err
}

func jenkinsReleaseMaterials(state domain.State, releasePlan domain.Object, runID string, controlPlaneBaseURL string) (domain.Object, domain.Object, error) {
	releasePlanID := stringValue(releasePlan["id"])
	applicationID := stringValue(releasePlan["applicationId"])
	projectID := stringValue(releasePlan["projectId"])
	if releasePlanID == "" || applicationID == "" || projectID == "" {
		return nil, nil, errors.New("JENKINS_RELEASE_PLAN_SNAPSHOT_INVALID")
	}
	buildSnapshot := cloneObject(objectMapFromAny(releasePlan["buildSnapshot"]))
	sourceSnapshot := cloneObject(objectMapFromAny(buildSnapshot["sourceSnapshot"]))
	if len(sourceSnapshot) == 0 {
		return nil, nil, errors.New("JENKINS_RELEASE_PLAN_SNAPSHOT_INVALID")
	}
	materialsSnapshot := domain.Object{
		"applicationId":  applicationID,
		"projectId":      projectID,
		"sourceSnapshot": sourceSnapshot,
	}
	materials, err := jenkinsReleaseMaterialsFromSnapshot(materialsSnapshot, releasePlanID, runID, controlPlaneBaseURL)
	return materialsSnapshot, materials, err
}

func (r *Runtime) JenkinsReleaseMaterials(run domain.Object) (domain.Object, error) {
	return jenkinsReleaseMaterialsFromSnapshot(
		objectMapFromAny(run["materialsSnapshot"]),
		stringValue(run["releasePlanId"]),
		stringValue(run["id"]),
		stringValue(run["controlPlaneBaseUrl"]),
	)
}

func jenkinsReleaseMaterialsFromSnapshot(snapshot domain.Object, releasePlanID string, runID string, controlPlaneBaseURL string) (domain.Object, error) {
	applicationID := stringValue(snapshot["applicationId"])
	projectID := stringValue(snapshot["projectId"])
	sourceSnapshot := objectMapFromAny(snapshot["sourceSnapshot"])
	if releasePlanID == "" || runID == "" || controlPlaneBaseURL == "" || applicationID == "" || projectID == "" {
		return nil, errors.New("JENKINS_RELEASE_MATERIALS_INVALID")
	}
	if firstNonEmptyString(stringValue(sourceSnapshot["provider"]), stringValue(sourceSnapshot["sourceType"])) != "git" || strings.TrimSpace(stringValue(sourceSnapshot["repositoryUrl"])) == "" {
		return nil, errors.New("JENKINS_RELEASE_SOURCE_INVALID")
	}
	runBaseURL := controlPlaneBaseURL + "/platform/jenkins/runs/" + runID
	sourceSnapshotPublic := domain.Object{
		"provider":        "git",
		"repositoryUrl":   stringValue(sourceSnapshot["repositoryUrl"]),
		"ref":             firstNonEmptyString(stringValue(sourceSnapshot["ref"]), stringValue(sourceSnapshot["resolvedRef"])),
		"refType":         stringValue(sourceSnapshot["refType"]),
		"jenkinsfilePath": firstNonEmptyString(stringValue(sourceSnapshot["jenkinsfilePath"]), "Jenkinsfile"),
		"sourcePath":      firstNonEmptyString(stringValue(sourceSnapshot["sourcePath"]), "."),
	}
	if resolved := stringValue(sourceSnapshot["resolvedRef"]); resolved != "" {
		sourceSnapshotPublic["resolvedRef"] = resolved
	}
	source := domain.Object{"snapshot": sourceSnapshotPublic}
	if stringValue(sourceSnapshot["authSecretRef"]) != "" {
		source["gitCredentialUrl"] = runBaseURL + "/git-credential"
	}
	deploy := domain.Object{
		"callbackUrl":         controlPlaneBaseURL + "/platform/jenkins/release-artifacts",
		"callbackTokenHeader": "x-iquops-jenkins-run-token",
	}
	materials := domain.Object{
		"runId":         runID,
		"releasePlanId": releasePlanID,
		"applicationId": applicationID,
		"projectId":     projectID,
		"source":        source,
		"deploy":        deploy,
	}
	return materials, nil
}

func jenkinsDeploymentTargets(state domain.State, swarmRuntime domain.Object) []domain.Object {
	result := []domain.Object{}
	seen := map[string]bool{}
	for _, service := range objectSlice(swarmRuntime["services"]) {
		serverID := stringValue(service["serverId"])
		if serverID == "" || seen[serverID] {
			continue
		}
		server := findObject(state.Servers, serverID)
		if server == nil {
			continue
		}
		seen[serverID] = true
		result = append(result, domain.Object{
			"serverId":       serverID,
			"targetPlatform": dockerTargetPlatform(server),
		})
	}
	return result
}

func dockerTargetPlatform(server domain.Object) string {
	profile := objectMapFromAny(server["platformProfile"])
	switch strings.ToLower(firstNonEmptyString(stringValue(profile["architecture"]), stringValue(server["architecture"]))) {
	case "arm64", "aarch64":
		return "linux/arm64"
	default:
		return "linux/amd64"
	}
}

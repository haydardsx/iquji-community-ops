package application

import (
	"context"
	"encoding/json"
	"errors"

	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const (
	applicationLogMaxLines       = 200
	applicationLogMaxBytes       = 256 * 1024
	applicationLogTimeout        = 10 * time.Second
	applicationContainerLogSince = "30m"
)

func (e *SystemApplicationRuntimeExecutor) Logs(ctx context.Context, input ApplicationServiceInput, slotKey string) (ApplicationServiceLogs, error) {
	ctx, cancel := context.WithTimeout(ctx, applicationLogTimeout)
	defer cancel()
	result := ApplicationServiceLogs{Availability: "available", CheckedAt: domain.Now(), Items: []ApplicationServiceLog{}}
	remote, err := e.dial(ctx, input)
	if err != nil {
		return result, err
	}
	defer remote.Close()
	// 日志按受管服务身份查询，已停止或失败任务的输出仍可读取，不依赖运行任务详情成功。
	snapshot, err := inspectApplicationServicesMode(ctx, remote, input, false)
	if err != nil {
		return result, err
	}
	if len(snapshot.Services) == 0 {
		result.Availability, result.UnavailableReason = "not_deployed", "APPLICATION_NOT_DEPLOYED"
		return result, nil
	}
	var target *ApplicationServiceState
	for i, service := range snapshot.Services {
		if service.ServiceSlotKey == slotKey || slotKey == "" && len(snapshot.Services) == 1 {
			if target != nil {
				return result, errors.New("APPLICATION_LOG_SERVICE_REQUIRED")
			}
			target = &snapshot.Services[i]
		}
	}
	if target == nil {
		return result, errors.New("APPLICATION_LOG_SERVICE_REQUIRED")
	}
	if target.LogDriver != "" && target.LogDriver != "json-file" && target.LogDriver != "journald" {
		return result, errors.New("APPLICATION_LOG_DRIVER_UNSUPPORTED")
	}
	command := "docker service logs --raw --timestamps --tail " + strconv.Itoa(applicationLogMaxLines) + " --since " + applicationContainerLogSince + " " + shellQuote(target.ServiceID)
	output, truncated, err := remote.RunLogs(ctx, command, applicationLogMaxBytes)
	if err != nil {
		if strings.Contains(output, "does not support reading") || strings.Contains(output, "configured logging driver") {
			return result, errors.New("APPLICATION_LOG_DRIVER_UNSUPPORTED")
		}
		return result, errors.New("APPLICATION_LOG_READ_FAILED")
	}
	result.Items, result.Truncated = parseApplicationServiceLogs(output, *target, input.LogSecrets, truncated)
	result.CheckedAt = domain.Now()
	boundApplicationServiceLogResponse(&result)
	return result, nil
}

func boundApplicationServiceLogResponse(result *ApplicationServiceLogs) {
	// HTTP 使用 json.Encoder.Encode，预留其额外写入的一个换行字节。
	const payloadLimit = applicationLogMaxBytes - 1
	encoded, _ := json.Marshal(result)
	if len(encoded) <= payloadLimit {
		return
	}
	result.Truncated = true
	empty := *result
	empty.Items = []ApplicationServiceLog{}
	encoded, _ = json.Marshal(empty)
	remaining := payloadLimit - len(encoded)
	start := len(result.Items)
	for i := len(result.Items) - 1; i >= 0; i-- {
		item, _ := json.Marshal(result.Items[i])
		required := len(item)
		if start < len(result.Items) {
			required++
		}
		if required > remaining {
			if start == len(result.Items) {
				message := result.Items[i].Message
				low, high := 0, len(message)
				for low < high {
					middle := (low + high + 1) / 2
					result.Items[i].Message = applicationLogPrefix(message, middle)
					item, _ = json.Marshal(result.Items[i])
					if len(item) <= remaining {
						low = middle
					} else {
						high = middle - 1
					}
				}
				result.Items[i].Message = applicationLogPrefix(message, low)
				start = i
			}
			break
		}
		remaining -= required
		start = i
	}
	result.Items = result.Items[start:]
}

func parseApplicationServiceLogs(output string, target ApplicationServiceState, secrets []string, truncated bool) ([]ApplicationServiceLog, bool) {
	if truncated {
		// 尾部缓冲可能从日志行中间开始；不回显无法确认完整边界的片段。
		first, _, _ := strings.Cut(output, "\n")
		token, _, found := strings.Cut(first, " ")
		_, timestampErr := time.Parse(time.RFC3339Nano, token)
		if !found || timestampErr != nil {
			if _, rest, hasNext := strings.Cut(output, "\n"); hasNext {
				output = rest
			} else {
				output = ""
			}
		}
	}
	output = domain.RedactLogText(output, secrets)
	items := []ApplicationServiceLog{}
	allTimed := true
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		message, timestamp := line, ""
		if token, rest, found := strings.Cut(line, " "); found {
			if parsed, err := time.Parse(time.RFC3339Nano, token); err == nil {
				timestamp, message = parsed.UTC().Format(time.RFC3339Nano), rest
			}
		}
		allTimed = allTimed && timestamp != ""
		items = append(items, ApplicationServiceLog{Message: message, Timestamp: timestamp, Source: "swarm", ServiceSlotKey: target.ServiceSlotKey})
	}
	if allTimed {
		sort.SliceStable(items, func(i, j int) bool {
			left, _ := time.Parse(time.RFC3339Nano, items[i].Timestamp)
			right, _ := time.Parse(time.RFC3339Nano, items[j].Timestamp)
			return left.Before(right)
		})
	}
	// Docker 的 tail 针对每个任务；在整个所选服务上再次限制，保留最新输出。
	if len(items) > applicationLogMaxLines {
		items = items[len(items)-applicationLogMaxLines:]
		truncated = true
	}
	total := 0
	for _, item := range items {
		total += len(item.Message) + len(item.Timestamp)
	}
	for len(items) > 1 && total > applicationLogMaxBytes {
		total -= len(items[0].Message) + len(items[0].Timestamp)
		items = items[1:]
		truncated = true
	}
	if len(items) == 1 && total > applicationLogMaxBytes {
		items[0].Message = applicationLogPrefix(items[0].Message, applicationLogMaxBytes-len(items[0].Timestamp))
		truncated = true
	}
	for i := range items {
		items[i].ID = target.ServiceID + ":" + domain.HashSecret(items[i].Timestamp + "\n" + items[i].Message)[:16] + ":" + strconv.Itoa(i)
	}
	return items, truncated
}

func applicationLogPrefix(value string, limit int) string {
	if limit < 0 {
		limit = 0
	}
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func (r *Runtime) applicationLogSecrets(ctx context.Context, applicationID, slot string) ([]string, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	refs := map[string]bool{}
	add := func(variables []domain.Object) {
		for _, variable := range variables {
			if variable["valueSource"] != "secret_ref" || variable["scope"] == "service" && slot != "" && stringValue(variable["serviceSlotKey"]) != slot {
				continue
			}
			if ref := firstNonEmptyString(stringValue(variable["materialRevisionRef"]), stringValue(variable["secretRef"])); ref != "" {
				refs[ref] = true
			}
		}
	}
	config := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
	variables := objectSlice(config["environmentVariables"])
	add(variables)
	for _, v := range variables {
		if v["valueSource"] != "output_ref" || v["scope"] == "service" && slot != "" && stringValue(v["serviceSlotKey"]) != slot {
			continue
		}
		owner, output, ok := visibleApplicationOutput(state, applicationID, stringValue(v["outputId"]))
		if !ok {
			return nil, errors.New("APPLICATION_LOG_REDACTION_UNAVAILABLE")
		}
		if sensitiveOutput(state, owner, output, map[string]bool{}) {
			value, err := resolveApplicationValue(state, applicationID, applicationID, v, map[string]bool{})
			if err != nil {
				return nil, errors.New("APPLICATION_LOG_REDACTION_UNAVAILABLE")
			}
			add([]domain.Object{value})
		}
	}
	// 同一资产的旧修订也可能出现在现存容器日志和历史构建摘要中。
	for _, asset := range state.SecretAssets {
		if asset["assetType"] != "application_secret" || !(refs[stringValue(asset["secretRef"])] || refs[stringValue(asset["materialRef"])]) {
			continue
		}
		for _, ref := range objectStringSlice(asset["revisionSecretRefs"]) {
			refs[ref] = true
		}
	}
	for _, task := range state.Tasks {
		if task.TargetID == applicationID {
			add(objectSlice(task.Payload["applicationInputs"]))
		}
	}
	for _, plan := range state.ReleasePlans {
		if stringValue(plan["applicationId"]) == applicationID {
			add(objectSlice(plan["configSnapshot"]))
		}
	}
	values := []string{}
	for ref := range refs {
		value, err := r.Store.GetSecretMaterial(ctx, ref)
		if err != nil {
			return nil, errors.New("APPLICATION_LOG_REDACTION_UNAVAILABLE")
		}
		if value != "" {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values, nil
}

package application

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func checkVolumePlacement(existing domain.Object, node string) error {
	if existing == nil {
		return nil
	}
	template := objectMapFromAny(objectMapFromAny(existing["Spec"])["TaskTemplate"])
	mounts := objectSlice(objectMapFromAny(template["ContainerSpec"])["Mounts"])
	if len(mounts) == 0 {
		return nil
	}
	if !containsString(objectStringSlice(objectMapFromAny(template["Placement"])["Constraints"]), "node.id=="+node) {
		return errors.New("APPLICATION_VOLUME_NODE_CHANGE_UNSUPPORTED")
	}
	for _, m := range mounts {
		if m["Type"] != "volume" {
			return errors.New("APPLICATION_VOLUME_OWNERSHIP_MISMATCH")
		}
	}
	return nil
}

func configuredServicePorts(swarm, svc domain.Object) []domain.Object {
	if ports := objectSlice(swarm["ports"]); len(ports) > 0 || explicitNoServicePorts(swarm) {
		return ports
	}
	published := int(numberValue(svc["publishedPort"]))
	if published == 0 {
		published = int(numberValue(swarm["publishedPort"]))
	}
	return []domain.Object{{"name": "web", "targetPort": int(numberOrDefault(swarm["containerPort"], 80)), "publishedPort": published, "protocol": "http"}}
}

func explicitNoServicePorts(swarm domain.Object) bool {
	_, declared := swarm["ports"]
	// 早期客户端会同时保存空数组和旧端口字段。
	return declared && len(objectSlice(swarm["ports"])) == 0 && swarm["containerPort"] == nil && swarm["publishedPort"] == nil
}

func buildServiceEndpoints(swarm, svc, labels domain.Object) []domain.Object {
	ports := []domain.Object{}
	var httpPort int
	for _, p := range configuredServicePorts(swarm, svc) {
		name, protocol, target := stringValue(p["name"]), stringValue(p["protocol"]), int(numberValue(p["targetPort"]))
		labels["iquops.port."+name] = protocol + "/" + strconv.Itoa(target)
		if protocol == "http" {
			if httpPort == 0 {
				httpPort = target
			}
			protocol = "tcp"
		}
		if n := int(numberValue(p["publishedPort"])); n > 0 {
			ports = append(ports, domain.Object{"Name": name, "Protocol": protocol, "TargetPort": target, "PublishedPort": n, "PublishMode": "ingress"})
		}
	}
	if httpPort > 0 {
		labels["traefik.enable"] = "true"
		labels["traefik.http.services."+safeName(stringValue(svc["serviceName"]))+".loadbalancer.server.port"] = strconv.Itoa(httpPort)
	} else {
		labels["traefik.enable"] = "false"
	}
	return ports
}

func containerServiceSpec(swarm, svc domain.Object, appID, nid, network, node string, artifact domain.Object, variables map[string]string, existing domain.Object) (domain.Object, error) {
	labels := domain.Object{"iquops.owner": "iquops", "iquops.application-id": appID, "iquops.service-slot": svc["slotKey"], "traefik.swarm.network": network}
	ports := buildServiceEndpoints(swarm, svc, labels)
	mounts := []domain.Object{}
	for _, v := range objectSlice(swarm["volumes"]) {
		mounts = append(mounts, domain.Object{"Type": "volume", "Source": physicalVolumeName(appID, stringValue(v["name"])), "Target": v["target"], "VolumeOptions": domain.Object{"Labels": domain.Object{"iquops.owner": "iquops", "iquops.application-id": appID}}})
	}
	env := make([]string, 0, len(variables))
	for k, v := range variables {
		if !applicationVariableName.MatchString(k) || strings.ContainsRune(v, 0) {
			return nil, errors.New("APPLICATION_VARIABLE_VALUE_INVALID")
		}
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	container := domain.Object{"Image": artifact["image"], "Env": env, "Mounts": mounts}
	if command, ok := stringArguments(swarm["command"]); ok {
		if swarm["shellEntrypoint"] == true {
			container["Command"], container["Args"] = command[:1], command[1:]
		} else {
			container["Args"] = command
		}
	}
	if health, ok := stringArguments(swarm["healthCommand"]); ok {
		container["Healthcheck"] = domain.Object{"Test": append([]string{"CMD"}, health...), "Interval": int64(10 * time.Second), "Timeout": int64(5 * time.Second), "StartPeriod": int64(30 * time.Second), "Retries": 3}
	}
	order, failure := "start-first", "rollback"
	if len(mounts) > 0 {
		order, failure = "stop-first", "pause"
	}
	task := domain.Object{"ContainerSpec": container, "Placement": domain.Object{"Constraints": []string{"node.id==" + node}}, "Networks": []domain.Object{{"Target": nid}}, "LogDriver": domain.Object{"Name": "json-file", "Options": domain.Object{"max-size": "10m", "max-file": "3"}}}
	if existing != nil {
		task["ForceUpdate"] = int(numberValue(objectMapFromAny(objectMapFromAny(existing["Spec"])["TaskTemplate"])["ForceUpdate"])) + 1
	}
	return domain.Object{"Name": svc["serviceName"], "Labels": labels, "TaskTemplate": task, "Mode": domain.Object{"Replicated": domain.Object{"Replicas": int(numberOrDefault(svc["replicas"], 1))}}, "UpdateConfig": domain.Object{"Order": order, "Parallelism": 1, "FailureAction": failure}, "RollbackConfig": domain.Object{"Order": "stop-first", "Parallelism": 1}, "EndpointSpec": domain.Object{"Mode": "vip", "Ports": ports}}, nil
}

func submitContainerService(ctx context.Context, peer containerPeer, spec, existing domain.Object) error {
	resource := "/services/create"
	creating := existing == nil
	if !creating {
		id := stringValue(existing["ID"])
		version := int64(numberValue(objectMapFromAny(existing["Version"])["Index"]))
		if !applicationResourceName.MatchString(id) || version < 1 {
			return errors.New("APPLICATION_SERVICE_VERSION_INVALID")
		}
		resource = "/services/" + id + "/update?version=" + strconv.FormatInt(version, 10)
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	// Docker Engine 使用服务版本执行乐观并发控制；凭据不进入远端命令文本。
	result, err := peer.call(ctx, "curl --fail --silent --show-error --max-time 30 --unix-socket /var/run/docker.sock -H 'Content-Type: application/json' --data-binary @- "+shellQuote("http://localhost"+resource), b, "APPLICATION_SERVICE_SUBMIT_FAILED")
	if err != nil {
		return err
	}
	if creating {
		var response struct{ ID string }
		if json.Unmarshal([]byte(result), &response) != nil || response.ID == "" {
			return errors.New("APPLICATION_SERVICE_SUBMIT_FAILED")
		}
	}
	return nil
}

func verifyContainerRelease(ctx context.Context, gateway, node containerPeer, svc, swarm domain.Object, digest string, generation int) error {
	name := stringValue(svc["serviceName"])
	desired := int(numberOrDefault(svc["replicas"], 1))
	verifyCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	// 回读当前任务、节点和实际镜像，防止把上一次发布仍在运行的副本认作成功。
	var ids []string
	for {
		raw, err := gateway.call(verifyCtx, "docker service inspect --format '{{if .UpdateStatus}}{{.UpdateStatus.State}}{{else}}created{{end}}' "+shellQuote(name)+" && docker service ps --no-trunc --filter desired-state=running --format '{{.ID}}' "+shellQuote(name), nil, "APPLICATION_TASKS_QUERY_FAILED")
		if err != nil {
			return err
		}
		update, list, _ := strings.Cut(raw, "\n")
		if update == "paused" || strings.HasPrefix(update, "rollback_") {
			return errors.New("APPLICATION_RELEASE_READBACK_FAILED")
		}
		tasks := strings.Fields(list)
		ids = nil
		if len(tasks) == desired {
			data, e := gateway.call(verifyCtx, "docker inspect --type task "+shellQuotedWords(tasks), nil, "APPLICATION_TASKS_QUERY_FAILED")
			if e != nil {
				return e
			}
			var rows []domain.Object
			if json.Unmarshal([]byte(data), &rows) != nil {
				return errors.New("APPLICATION_TASKS_QUERY_FAILED")
			}
			for _, t := range rows {
				status := objectMapFromAny(t["Status"])
				containerID := stringValue(objectMapFromAny(status["ContainerStatus"])["ContainerID"])
				if int(numberValue(objectMapFromAny(t["Spec"])["ForceUpdate"])) != generation || status["State"] != "running" || stringValue(t["NodeID"]) != node.nodeID || !applicationResourceName.MatchString(containerID) {
					break
				}
				info, e := node.call(verifyCtx, "docker container inspect "+shellQuote(containerID)+" --format '{{.Image}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}'", nil, "APPLICATION_CONTAINER_QUERY_FAILED")
				if e != nil {
					return e
				}
				imageID, health, _ := strings.Cut(info, "|")
				if imageID != digest || (health != "" && health != "healthy") {
					break
				}
				ids = append(ids, containerID)
			}
		}
		if len(ids) == desired {
			break
		}
		select {
		case <-verifyCtx.Done():
			return errors.New("APPLICATION_RELEASE_HEALTH_TIMEOUT")
		case <-time.After(time.Second * 2):
		}
	}
	if init, ok := stringArguments(swarm["initializeCommand"]); ok {
		for _, id := range ids {
			if _, err := node.call(verifyCtx, "docker exec "+shellQuote(id)+" "+shellQuotedWords(init)+" >/dev/null 2>&1", nil, "APPLICATION_INITIALIZATION_FAILED"); err != nil {
				return err
			}
			if probe, ok := stringArguments(swarm["healthCommand"]); ok {
				if _, err := node.call(verifyCtx, "docker exec "+shellQuote(id)+" "+shellQuotedWords(probe)+" >/dev/null 2>&1", nil, "APPLICATION_RELEASE_HEALTH_FAILED"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func observedServicePorts(labels, endpoint domain.Object) []domain.Object {
	result := []domain.Object{}
	for key, raw := range labels {
		name, isPort := strings.CutPrefix(key, "iquops.port.")
		if !isPort || !portName.MatchString(name) {
			continue
		}
		scheme, targetStr, ok := strings.Cut(stringValue(raw), "/")
		target, err := strconv.Atoi(targetStr)
		if !ok || err != nil || target <= 0 || target > 65535 || (scheme != "http" && scheme != "tcp" && scheme != "udp") {
			continue
		}
		port := domain.Object{"name": name, "protocol": scheme, "containerPort": target}
		transport := scheme
		if transport == "http" {
			transport = "tcp"
		}
		for _, p := range objectSlice(endpoint["Ports"]) {
			if int(numberValue(p["TargetPort"])) == target && firstNonEmptyString(stringValue(p["Protocol"]), "tcp") == transport {
				port["publishedPort"] = p["PublishedPort"]
				break
			}
		}
		result = append(result, port)
	}
	sort.Slice(result, func(i, j int) bool { return stringValue(result[i]["name"]) < stringValue(result[j]["name"]) })
	return result
}

package application

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

type jenkinsAccessEndpoint interface {
	close()
	broker(context.Context, string, domain.Object) (domain.Object, error)
	publish(context.Context, string, string) error
	remove(context.Context) error
}

type jenkinsAccessRemote struct {
	gateway, build *ssh.Client
	container      string
	route          RouteRuntimeInput
	binding        domain.Object
}

func (r *Runtime) openJenkinsAccess(ctx context.Context, state domain.State, serverID string, binding domain.Object, task domain.Task) (jenkinsAccessEndpoint, error) {
	build := findObject(state.Servers, serverID)
	gateway := findObject(state.Servers, stringValue(binding["gatewayServerId"]))
	connect := func(s domain.Object) (*ssh.Client, error) {
		key, ok := r.serverSSHSecretMaterial(ctx, state, s)
		if !ok {
			return nil, errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
		}
		return r.openTerminalSSH(ctx, s, key)
	}
	b, err := connect(build)
	if err != nil {
		return nil, err
	}
	g, err := connect(gateway)
	if err != nil {
		b.Close()
		return nil, err
	}
	id := stringValue(binding["bindingId"])
	asset := domain.Object{"name": binding["host"], "id": binding["domainId"], "certificateId": binding["certificateId"], "tls": true}
	route := RouteRuntimeInput{
		CertResolver: r.Config.TraefikCertResolver,

		Route:       domain.Object{"id": "jenkins-" + id, "priority": binding["priority"]},
		Gateway:     gateway,
		TraefikName: r.Config.TraefikService,
		Network:     r.Config.SwarmNetwork,
		ClusterID:   stringValue(serverRuntimeMetrics(gateway)["swarmClusterId"]),
		Revision:    routeRevision(id),
		HTTPSOnly:   true,
		HTTPSPort:   numberValue(binding["httpsPort"]),
		Domains:     []domain.Object{asset},
		Rules:       []domain.Object{{"path": "/"}},
		Log: func(source, phase, level, message string) {
			r.appendRuntimeMessage(task, source, phase, level, message)
		},
	}
	return &jenkinsAccessRemote{
		gateway:   g,
		build:     b,
		container: firstNonEmptyString(stringValue(serverRuntimeMetrics(build)["jenkinsContainer"]), r.Config.JenkinsContainer),
		route:     route,
		binding:   binding,
	}, nil
}

func (j *jenkinsAccessRemote) close() {
	j.gateway.Close()
	j.build.Close()
}

func (j *jenkinsAccessRemote) broker(ctx context.Context, action string, body domain.Object) (domain.Object, error) {
	data, _ := json.Marshal(body)
	out, err := remoteOutputWithInput(ctx, j.build, jenkinsBrokerCommand(j.container, action, stringValue(j.binding["bindingId"])), data)
	var result domain.Object
	if err != nil || json.Unmarshal([]byte(out), &result) != nil {
		return nil, errors.New("JENKINS_ACCESS_BROKER_FAILED")
	}
	return result, nil
}

func (j *jenkinsAccessRemote) publish(ctx context.Context, buildHost, password string) error {
	if err := inspectRouteGateway(ctx, j.gateway, j.route); err != nil {
		return err
	}
	ca, server, client, err := jenkinsAccessIdentities()
	if err != nil {
		return err
	}
	endpoint, err := j.broker(ctx, "access-open", domain.Object{"ca": ca, "cert": server.cert, "key": server.key})
	if err != nil {
		return err
	}
	port := numberValue(endpoint["port"])
	if port < 1 || port > 65535 || buildHost == "" {
		return errors.New("JENKINS_ACCESS_BROKER_FAILED")
	}
	dir := "jenkins-" + j.route.Revision
	for name, value := range map[string]string{"ca.pem": ca, "cert.pem": client.cert, "key.pem": client.key} {
		if err := writeRemoteFile(ctx, j.gateway, traefikRemoteRoot+"/certificates/"+dir+"/"+name, value); err != nil {
			return errors.New("ROUTE_CONFIG_UPLOAD_FAILED")
		}
	}
	content, err := jenkinsAccessRoute(j.route, dir, buildHost, port)
	if err != nil {
		return err
	}
	path, staged, _ := routeRemotePaths(stringValue(j.route.Route["id"]))
	if err = writeRemoteFileBytes(ctx, j.gateway, staged, content); err != nil {
		return errors.New("ROUTE_CONFIG_UPLOAD_FAILED")
	}
	if err = remoteRun(ctx, j.gateway, "mv -- "+shellQuote(staged)+" "+shellQuote(path)); err != nil {
		return errors.New("ROUTE_CONFIG_REPLACE_FAILED")
	}
	if _, err = verifyRouteAccess(ctx, j.gateway, j.route, false); err != nil {
		return err
	}
	if _, err = j.broker(ctx, "access-viewer", domain.Object{"password": password, "url": j.binding["url"]}); err != nil {
		return err
	}
	return verifyJenkinsAccess(ctx, j.gateway, j.route, password)
}

func jenkinsAccessRoute(route RouteRuntimeInput, dir, buildHost string, port int) ([]byte, error) {
	prefix := "iquops-" + routeRevision(route.Route["id"])
	backend := (&url.URL{Scheme: "https", Host: net.JoinHostPort(strings.Trim(buildHost, "[]"), strconv.Itoa(port))}).String()
	certDir := "/certificates/" + dir + "/"
	router := domain.Object{
		"entryPoints": []string{"websecure"},
		"priority":    numberValue(route.Route["priority"]),
		"rule":        "Host(" + strconv.Quote(stringValue(route.Domains[0]["name"])) + ")",
		"service":     prefix,
		"middlewares": []string{prefix},
		"tls":         domain.Object{"certResolver": managedTraefikResolver(route.CertResolver)},
	}
	transport := domain.Object{
		"serverName":   "jenkins-forward.internal",
		"rootCAs":      []string{certDir + "ca.pem"},
		"certificates": []domain.Object{{"certFile": certDir + "cert.pem", "keyFile": certDir + "key.pem"}},
	}
	headers := domain.Object{"customResponseHeaders": domain.Object{"X-IQuOps-Route": routeRevision(route.Route["id"]) + ":" + route.Revision}}
	balancer := domain.Object{"servers": []domain.Object{{"url": backend}}, "serversTransport": prefix}
	return json.Marshal(domain.Object{"http": domain.Object{
		"routers":           domain.Object{prefix: router},
		"middlewares":       domain.Object{prefix: domain.Object{"headers": headers}},
		"services":          domain.Object{prefix: domain.Object{"loadBalancer": balancer}},
		"serversTransports": domain.Object{prefix: transport},
	}})
}

func (j *jenkinsAccessRemote) remove(ctx context.Context) error {
	// 先撤销构建机端的转发，网关暂不可用时也能阻断旧客户端。
	_, brokerErr := j.broker(ctx, "access-close", domain.Object{})
	path, staged, _ := routeRemotePaths(stringValue(j.route.Route["id"]))
	certDir := traefikRemoteRoot + "/certificates/jenkins-" + j.route.Revision
	if err := remoteRun(ctx, j.gateway, "rm -f -- "+shellQuote(path)+" "+shellQuote(staged)+" && rm -rf -- "+shellQuote(certDir)); err != nil {
		return errors.Join(brokerErr, errors.New("ROUTE_CONFIG_REMOVE_FAILED"))
	}
	_, verifyErr := verifyRouteAccess(ctx, j.gateway, j.route, true)
	return errors.Join(brokerErr, verifyErr)
}

func (r *Runtime) executeJenkinsAccess(ctx context.Context, task domain.Task) (string, error) {
	err := r.changeJenkinsAccess(r.WithServerConnections(ctx), task)
	if err != nil {
		finish, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		_ = r.Store.MutateState(finish, func(state *domain.State) error {
			server := findObject(state.Servers, task.TargetID)
			if server != nil {
				access := objectMapFromAny(server["jenkinsAccess"])
				access["status"] = "failed"
				if domain.TaskAction(task.Action) == domain.TaskActionBindJenkinsAccess && len(objectMapFromAny(access["applied"])) > 0 && len(objectMapFromAny(access["pending"])) == 0 {
					access["status"] = "active"
				}
				server["jenkinsAccess"] = access
			}
			return nil
		})
		return "Jenkins 访问入口变更失败", err
	}
	return "Jenkins 访问入口已核验", nil
}

func (r *Runtime) changeJenkinsAccess(ctx context.Context, task domain.Task) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	return r.changeJenkinsAccessWith(ctx, task, r.openJenkinsAccess)
}

type jenkinsAccessOpener func(context.Context, domain.State, string, domain.Object, domain.Task) (jenkinsAccessEndpoint, error)

func (r *Runtime) changeJenkinsAccessWith(ctx context.Context, task domain.Task, open jenkinsAccessOpener) error {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	server := findObject(state.Servers, task.TargetID)
	if server == nil {
		return errors.New("SERVER_NOT_FOUND")
	}
	access := objectMapFromAny(server["jenkinsAccess"])
	old := objectMapFromAny(access["applied"])
	remove := func(c context.Context, snap domain.Object) error {
		remote, err := open(c, state, task.TargetID, snap, task)
		if err != nil {
			return err
		}
		defer remote.close()
		return remote.remove(c)
	}
	patch := func(c context.Context, fn func(domain.Object)) error {
		return r.Store.MutateState(c, func(st *domain.State) error {
			srv := findObject(st.Servers, task.TargetID)
			if srv == nil {
				return errors.New("SERVER_NOT_FOUND")
			}
			current := objectMapFromAny(srv["jenkinsAccess"])
			fn(current)
			srv["jenkinsAccess"] = current
			return nil
		})
	}
	restoreViewer := func(c context.Context) error {
		if len(old) == 0 {
			return nil
		}
		previous, err := r.Store.GetSecretMaterial(c, stringValue(old["viewerSecretRef"]))
		if err != nil {
			return err
		}
		prior, err := open(c, state, task.TargetID, old, task)
		if err != nil {
			return err
		}
		defer prior.close()
		_, err = prior.broker(c, "access-viewer", domain.Object{"password": previous, "url": old["url"]})
		return err
	}
	if retiring := objectMapFromAny(access["retiring"]); len(retiring) > 0 {
		if err := remove(ctx, retiring); err != nil {
			return err
		}
		if err := patch(ctx, func(a domain.Object) { delete(a, "retiring") }); err != nil {
			return err
		}
	}
	if pending := objectMapFromAny(access["pending"]); len(pending) > 0 {
		if domain.TaskAction(task.Action) == domain.TaskActionBindJenkinsAccess {
			if err := restoreViewer(ctx); err != nil {
				return err
			}
		}
		if err := remove(ctx, pending); err != nil {
			return err
		}
		if err := patch(ctx, func(a domain.Object) { delete(a, "pending") }); err != nil {
			return err
		}
	}
	if domain.TaskAction(task.Action) != domain.TaskActionBindJenkinsAccess {
		if len(old) > 0 {
			if err := remove(ctx, old); err != nil {
				return err
			}
		}
		return r.Store.MutateStateWithSecretChanges(ctx, nil, []string{stringValue(old["viewerSecretRef"])}, func(st *domain.State) error {
			srv := findObject(st.Servers, task.TargetID)
			delete(srv, "jenkinsAccess")
			return nil
		})
	}
	if old["bindingId"] == task.ID {
		return nil
	}
	next, err := r.jenkinsAccessBinding(state, task.TargetID, stringValue(task.Payload["domainId"]), stringValue(task.Payload["gatewayServerId"]))
	if err != nil {
		return err
	}
	password, err := r.Store.GetSecretMaterial(ctx, stringValue(task.Payload["stagedSecretRef"]))
	if err != nil {
		return err
	}
	next["bindingId"] = task.ID
	next["priority"] = numberOrDefault(old["priority"], 1000) + 1
	err = r.Store.MutateState(ctx, func(st *domain.State) error {
		if _, e := r.jenkinsAccessBinding(*st, task.TargetID, stringValue(next["domainId"]), stringValue(next["gatewayServerId"])); e != nil {
			return e
		}
		srv := findObject(st.Servers, task.TargetID)
		current := objectMapFromAny(srv["jenkinsAccess"])
		current["pending"], current["status"] = next, "pending"
		srv["jenkinsAccess"] = current
		return nil
	})
	if err != nil {
		return err
	}
	remote, err := open(ctx, state, task.TargetID, next, task)
	if err != nil {
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return errors.Join(err, patch(finish, func(a domain.Object) { delete(a, "pending") }))
	}
	defer remote.close()
	rollback := func(cause error) error {
		recovery, stop := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer stop()
		// 无论候选网关能否立即回收，旧入口都先恢复其独立口令与 Jenkins 根地址。
		restoreErr := restoreViewer(recovery)
		cleanErr := remote.remove(recovery)
		if restoreErr != nil || cleanErr != nil {
			return errors.Join(cause, restoreErr, cleanErr)
		}
		return errors.Join(cause, patch(recovery, func(a domain.Object) { delete(a, "pending") }))
	}
	buildHost := firstNonEmptyString(stringValue(server["privateHost"]), serverPublicHost(server))
	if err := remote.publish(ctx, buildHost, password); err != nil {
		return rollback(err)
	}
	// 先持久化已通过登录校验的新入口及待回收的旧入口，再变更旧远端。
	// 重启或清理失败时保持新的可用入口，重试只做剩余回收。
	next["viewerSecretRef"] = domain.NewID("secret")
	err = r.Store.MutateStateWithSecretChanges(ctx, map[string]string{stringValue(next["viewerSecretRef"]): password}, []string{stringValue(old["viewerSecretRef"])}, func(st *domain.State) error {
		srv := findObject(st.Servers, task.TargetID)
		current := domain.Object{"applied": next, "status": "active"}
		if len(old) > 0 {
			retiring := cloneObject(old)
			delete(retiring, "viewerSecretRef")
			current["retiring"] = retiring
		}
		srv["jenkinsAccess"] = current
		return nil
	})
	if err != nil {
		return rollback(err)
	}
	if len(old) > 0 {
		if err = remove(ctx, old); err != nil {
			return err
		}
		return patch(ctx, func(a domain.Object) { delete(a, "retiring") })
	}
	return nil
}

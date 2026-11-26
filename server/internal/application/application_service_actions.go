package application

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

func (e *SystemApplicationRuntimeExecutor) Apply(ctx context.Context, input ApplicationServiceInput, operation string, removeVolumes bool) (ApplicationRuntimeSnapshot, error) {
	if removeVolumes {
		return ApplicationRuntimeSnapshot{}, errors.New("APPLICATION_VOLUME_REMOVAL_UNSUPPORTED")
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	remote, err := e.dial(ctx, input)
	if err != nil {
		return ApplicationRuntimeSnapshot{}, err
	}
	defer func() { _ = remote.Close() }()
	before, err := inspectApplicationServices(ctx, remote, input)
	if err != nil {
		return before, err
	}
	if len(before.Services) == 0 {
		if operation == "uninstall" {
			return before, nil
		}
		return before, errors.New("APPLICATION_NOT_DEPLOYED")
	}
	targets, err := applicationServiceOperationTargets(before, input, operation)
	if err != nil {
		return before, err
	}
	for _, service := range before.Services {
		command, err := applicationServiceActionCommand(input.ApplicationID, service, operation, targets[service.ServiceID])
		if err != nil {
			return before, err
		}
		if command == "" {
			continue
		}
		var applyErr error
		if values, apply := input.Environment[service.ServiceSlotKey]; apply && (operation == "start" || operation == "restart") {
			writer, ok := remote.(interface {
				applyEnvironment(context.Context, string, ApplicationServiceState, map[string]string, []string, int) error
			})
			if !ok {
				return before, errors.New("APPLICATION_RUNTIME_ENVIRONMENT_UNAVAILABLE")
			}
			applyErr = writer.applyEnvironment(ctx, input.ApplicationID, service, values, input.PreviousInputKeys, targets[service.ServiceID])
		} else {
			_, _, applyErr = remote.Run(ctx, command, 65536)
		}
		if applyErr != nil {
			current, queryErr := inspectApplicationServices(ctx, remote, input)
			if queryErr == nil {
				return current, errors.New("APPLICATION_RUNTIME_ACTION_PARTIALLY_FAILED")
			}
			return before, errors.New("APPLICATION_RUNTIME_ACTION_FAILED")
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	latest := before
	for {
		current, err := inspectApplicationServices(ctx, remote, input)
		if err != nil {
			return latest, err
		}
		latest = current
		if applicationOperationConverged(before, current, operation, targets) {
			return current, nil
		}
		select {
		case <-ctx.Done():
			return latest, errors.New("APPLICATION_RUNTIME_CONVERGENCE_TIMEOUT")
		case <-ticker.C:
		}
	}
}

func applicationServiceOperationTargets(before ApplicationRuntimeSnapshot, input ApplicationServiceInput, operation string) (map[string]int, error) {
	if operation != "start" && operation != "stop" && operation != "restart" && operation != "uninstall" {
		return nil, errors.New("APPLICATION_ACTION_UNSUPPORTED")
	}
	targets := map[string]int{}
	anyRunning := false
	for _, service := range before.Services {
		target := service.DesiredReplicas
		switch operation {
		case "start":
			target = service.ExpectedReplicas
			if target < 1 {
				return nil, errors.New("APPLICATION_SERVICE_REPLICAS_REQUIRED")
			}
		case "stop", "uninstall":
			target = 0
		}
		anyRunning = anyRunning || service.DesiredReplicas > 0
		targets[service.ServiceID] = target
	}
	if operation == "start" {
		for _, target := range input.Services {
			found := false
			for _, service := range before.Services {
				found = found || service.ServiceName == target.ServiceName
			}
			if !found {
				return nil, errors.New("APPLICATION_SERVICE_NOT_DEPLOYED")
			}
		}
	}
	if operation == "restart" && !anyRunning {
		return nil, errors.New("APPLICATION_STOPPED_USE_START")
	}
	return targets, nil
}

func applicationServiceActionCommand(applicationID string, service ApplicationServiceState, operation string, replicas int) (string, error) {
	id := shellQuote(service.ServiceID)
	guard := "test \"$(docker service inspect " + id + " --format '{{index .Spec.Labels \"iquops.owner\"}}')\" = iquops && test \"$(docker service inspect " + id + " --format '{{index .Spec.Labels \"iquops.application-id\"}}')\" = " + shellQuote(applicationID) + " && "
	switch operation {
	case "start", "stop":
		return guard + "docker service scale --detach " + shellQuote(service.ServiceID+"="+strconv.Itoa(replicas)), nil
	case "restart":
		if service.DesiredReplicas == 0 {
			return "", nil
		}
		return guard + "docker service update --detach --force " + id, nil
	case "uninstall":
		return guard + "docker service rm " + id, nil
	default:
		return "", errors.New("APPLICATION_ACTION_UNSUPPORTED")
	}
}

func applicationOperationConverged(before, after ApplicationRuntimeSnapshot, operation string, targets map[string]int) bool {
	if operation == "uninstall" {
		return len(after.Services) == 0
	}
	if len(before.Services) != len(after.Services) {
		return false
	}
	for _, service := range after.Services {
		target, exists := targets[service.ServiceID]
		if !exists || service.DesiredReplicas != target || service.RunningReplicas != target {
			return false
		}
		if service.UpdateState == "paused" || service.UpdateState == "rollback_paused" {
			return false
		}
		if operation == "restart" && target > 0 {
			if service.UpdateState != "completed" {
				return false
			}
			oldTasks := map[string]bool{}
			for _, old := range before.Services {
				if old.ServiceID == service.ServiceID {
					for _, task := range old.Tasks {
						if task.Status == "running" {
							oldTasks[task.ID] = true
						}
					}
				}
			}
			for _, task := range service.Tasks {
				if task.Status == "running" && oldTasks[task.ID] {
					return false
				}
			}
		}
	}
	return true
}

type applicationSSHRemote struct{ client *ssh.Client }

func openApplicationServiceRemote(ctx context.Context, input ApplicationServiceInput) (applicationServiceRemote, error) {
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	return &applicationSSHRemote{client: client}, nil
}

func (r *applicationSSHRemote) Close() error { return r.client.Close() }

// Run 同时限制标准输出和错误输出的总内存，避免单行日志绕过 tail 行数限制。
func (r *applicationSSHRemote) Run(ctx context.Context, command string, limit int) (string, bool, error) {
	return r.runBounded(ctx, command, limit, false)
}

func (r *applicationSSHRemote) RunLogs(ctx context.Context, command string, limit int) (string, bool, error) {
	return r.runBounded(ctx, command, limit, true)
}

func (r *applicationSSHRemote) runBounded(ctx context.Context, command string, limit int, keepTail bool) (string, bool, error) {
	session, err := r.client.NewSession()
	if err != nil {
		return "", false, err
	}
	defer func() { _ = session.Close() }()
	writer := &applicationBoundedOutput{limit: limit, keepTail: keepTail}
	session.Stdout, session.Stderr = writer, writer
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		err = ctx.Err()
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return strings.TrimSpace(writer.buffer.String()), writer.truncated, err
}

type applicationBoundedOutput struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
	keepTail  bool
}

func (w *applicationBoundedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.keepTail {
		if w.limit < 1 {
			w.truncated = w.truncated || len(p) > 0
			return len(p), nil
		}
		excess := w.buffer.Len() + len(p) - w.limit
		if excess > 0 {
			w.truncated = true
		}
		if len(p) >= w.limit {
			w.buffer.Reset()
			_, _ = w.buffer.Write(p[len(p)-w.limit:])
		} else {
			if excess > 0 {
				w.buffer.Next(excess)
			}
			_, _ = w.buffer.Write(p)
		}
		return len(p), nil
	}
	remaining := w.limit - w.buffer.Len()
	if remaining < len(p) {
		w.truncated = true
	}
	if remaining > 0 {
		_, _ = w.buffer.Write(p[:minInt(remaining, len(p))])
	}
	return len(p), nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

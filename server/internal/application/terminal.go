package application

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// TerminalTarget 只保存控制面核验过的身份，票据不携带密钥或连接地址。
type TerminalTarget struct {
	ServerID      string
	ApplicationID string
	TaskID        string
	ServiceID     string
	NodeID        string
}

func (r *Runtime) ResolveTerminalTarget(ctx context.Context, kind, id, taskID string) (TerminalTarget, error) {
	if id == "" {
		return TerminalTarget{}, errors.New("TERMINAL_TARGET_INVALID")
	}
	if kind == "server" && taskID == "" {
		state, err := r.Store.LoadState(ctx)
		if err != nil {
			return TerminalTarget{}, err
		}
		if findObject(state.Servers, id) == nil {
			return TerminalTarget{}, errors.New("SERVER_NOT_FOUND")
		}
		return TerminalTarget{ServerID: id}, nil
	}
	if kind != "application" || taskID == "" {
		return TerminalTarget{}, errors.New("TERMINAL_TARGET_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	input, err := r.applicationServiceInput(ctx, id, nil)
	if err != nil {
		return TerminalTarget{}, err
	}
	snapshot, err := r.ApplicationRuntimeExecutor.Inspect(ctx, input)
	if err != nil {
		return TerminalTarget{}, err
	}
	for _, service := range snapshot.Services {
		for _, task := range service.Tasks {
			if task.ID == taskID && task.Status == "running" {
				if task.ServerID == "" {
					return TerminalTarget{}, errors.New("TERMINAL_NODE_UNAVAILABLE")
				}
				return TerminalTarget{ServerID: task.ServerID, ApplicationID: id, TaskID: task.ID, ServiceID: service.ServiceID, NodeID: task.NodeID}, nil
			}
		}
	}
	return TerminalTarget{}, errors.New("TERMINAL_TASK_CHANGED")
}

type TerminalSession struct {
	client  *ssh.Client
	session *ssh.Session
	Input   io.WriteCloser
	exited  <-chan error
	cleanup func()
}

func (s *TerminalSession) Close() {
	if s.cleanup != nil {
		s.cleanup()
	}
	_ = s.client.Close()
}
func (s *TerminalSession) Wait() error                 { return <-s.exited }
func (s *TerminalSession) Resize(cols, rows int) error { return s.session.WindowChange(rows, cols) }

var containerIdentity = regexp.MustCompile("^[a-f0-9]{64}$")

func resolveContainerTerminal(ctx context.Context, client *ssh.Client, target TerminalTarget) (containerTerminal, error) {
	// 节点本地的容器 ID 不复用；副本在核验后迁移时，exec 只会在原容器失败。
	id, err := resolveSwarmContainerID(ctx, &applicationSSHRemote{client: client}, target)
	if err != nil {
		return containerTerminal{}, err
	}
	for _, shell := range []string{"/bin/sh", "/bin/bash"} {
		_, _, err = terminalProbe(ctx, client, "docker exec "+id+" "+shell+" -c :")
		if err == nil {
			return containerTerminal{id, shell}, nil
		}
		if ctx.Err() != nil {
			return containerTerminal{}, ctx.Err()
		}
	}
	return containerTerminal{}, errors.New("TERMINAL_CONTAINER_SHELL_MISSING")
}

// 每次在任务实际所在节点回读容器 ID，避免向已迁移或其他应用的副本发出命令。
func resolveSwarmContainerID(ctx context.Context, remote applicationServiceRemote, target TerminalTarget) (string, error) {
	filter := func(label, value string) string {
		return " --filter " + shellQuote("label=com.docker.swarm."+label+"="+value)
	}
	query := "docker ps --no-trunc -q" + filter("task.id", target.TaskID) + filter("service.id", target.ServiceID) + filter("node.id", target.NodeID)
	result, truncated, err := remote.Run(ctx, query, 1024)
	id := strings.TrimSpace(result)
	if err != nil || truncated || !containerIdentity.MatchString(id) {
		return "", errors.New("TERMINAL_TASK_CHANGED")
	}
	return id, nil
}

func terminalProbe(ctx context.Context, client *ssh.Client, command string) (string, bool, error) {
	remote := &applicationSSHRemote{client: client}
	return remote.Run(ctx, command, 1024)
}

func (r *Runtime) OpenTerminal(ctx context.Context, target TerminalTarget, cols, rows int, output io.Writer) (*TerminalSession, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	server := findObject(state.Servers, target.ServerID)
	if server == nil {
		return nil, errors.New("SERVER_NOT_FOUND")
	}
	key, ok := r.serverSSHSecretMaterial(ctx, state, server)
	if !ok {
		return nil, errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
	}
	client, err := r.openTerminalSSH(ctx, server, key)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	var container containerTerminal
	var identity *containerTerminalOutput
	if target.TaskID != "" {
		container, err = resolveContainerTerminal(ctx, client, target)
		if err != nil {
			_ = client.Close()
			return nil, err
		}
		identity = &containerTerminalOutput{leader: make(chan terminalLeader, 1), output: output}
		output = identity
	}
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, errors.New("TERMINAL_SESSION_FAILED")
	}
	session.Stdout, session.Stderr = output, output
	input, err := session.StdinPipe()
	if err == nil {
		err = session.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{})
	}
	if err == nil {
		if identity == nil {
			err = session.Shell()
		} else {
			err = session.Start(container.startCommand())
		}
	}
	if err != nil {
		_ = client.Close()
		return nil, errors.New("TERMINAL_SESSION_FAILED")
	}
	exited := make(chan error, 1)
	go func() { exited <- session.Wait() }()
	result := &TerminalSession{client: client, session: session, Input: input, exited: exited}
	if identity != nil {
		select {
		case leader := <-identity.leader:
			result.cleanup, err = container.watch(client, leader)
			if err != nil {
				result.Close()
				return nil, errors.New("TERMINAL_SESSION_FAILED")
			}
			if _, err = io.WriteString(input, "\n"); err != nil {
				result.Close()
				return nil, errors.New("TERMINAL_SESSION_FAILED")
			}
		case <-exited:
			_ = client.Close()
			return nil, errors.New("TERMINAL_SESSION_FAILED")
		case <-ctx.Done():
			_ = client.Close()
			return nil, ctx.Err()
		}
	}
	return result, nil
}

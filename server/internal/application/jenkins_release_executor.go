package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

const (
	jenkinsReleaseFolder = domain.ManagedJenkinsFolder
	jenkinsReleaseJob    = domain.ManagedJenkinsJob
)

type JenkinsReleaseExecutor interface {
	OpenCallbackTunnel(ctx context.Context, input JenkinsCallbackTunnelInput) (JenkinsCallbackTunnel, error)
}

type JenkinsCallbackTunnelInput struct {
	Server           domain.Object
	SSHKey           string
	JenkinsContainer string
	JenkinsPort      int
	LocalAPIAddress  string
}

type JenkinsReleaseTriggerInput struct {
	RunID               string
	ReleasePlanID       string
	ApplicationID       string
	JenkinsPort         int
	RunToken            string
	ControlPlaneBaseURL string
	Materials           domain.Object
	ExpiresAt           string
}

type JenkinsCallbackTunnel interface {
	ControlPlaneBaseURL() string
	Validate(ctx context.Context, runID string, runToken string) error
	Trigger(ctx context.Context, input JenkinsReleaseTriggerInput) (domain.Object, error)
	Cancel(ctx context.Context, trigger domain.Object) error
	Start(ctx context.Context, runID string) (domain.Object, error)
	Status(ctx context.Context, runID string) (domain.Object, error)
	Errors() <-chan error
	Close() error
}

type SystemJenkinsReleaseExecutor struct{}

func NewSystemJenkinsReleaseExecutor() *SystemJenkinsReleaseExecutor {
	return &SystemJenkinsReleaseExecutor{}
}

func (e *SystemJenkinsReleaseExecutor) OpenCallbackTunnel(ctx context.Context, input JenkinsCallbackTunnelInput) (JenkinsCallbackTunnel, error) {
	if strings.TrimSpace(input.LocalAPIAddress) == "" {
		return nil, errors.New("JENKINS_CALLBACK_LOCAL_API_REQUIRED")
	}
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("JENKINS_CALLBACK_TUNNEL_UNAVAILABLE")
	}
	listener, err := client.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = client.Close()
		return nil, errors.New("JENKINS_CALLBACK_TUNNEL_UNAVAILABLE")
	}
	port, err := listenerPort(listener.Addr())
	if err != nil {
		_ = listener.Close()
		_ = client.Close()
		return nil, err
	}
	tunnel := &systemJenkinsCallbackTunnel{
		client:           client,
		listener:         listener,
		localAPIAddress:  input.LocalAPIAddress,
		jenkinsContainer: firstNonEmptyString(input.JenkinsContainer, "iquops-jenkins"),
		jenkinsPort:      positiveRuntimePort(input.JenkinsPort, 58080),
		controlPlaneURL:  "http://127.0.0.1:" + strconv.Itoa(port),
		errors:           make(chan error, 1),
		closed:           make(chan struct{}),
	}
	go tunnel.serve()
	return tunnel, nil
}

type systemJenkinsCallbackTunnel struct {
	client           *ssh.Client
	listener         net.Listener
	localAPIAddress  string
	jenkinsContainer string
	jenkinsPort      int
	controlPlaneURL  string
	errors           chan error
	closed           chan struct{}
	closeOnce        sync.Once
}

func (t *systemJenkinsCallbackTunnel) ControlPlaneBaseURL() string {
	return t.controlPlaneURL
}

func (t *systemJenkinsCallbackTunnel) Errors() <-chan error {
	return t.errors
}

func (t *systemJenkinsCallbackTunnel) Close() error {
	var closeErr error
	t.closeOnce.Do(func() {
		close(t.closed)
		if t.listener != nil {
			closeErr = t.listener.Close()
		}
		if t.client != nil {
			if err := t.client.Close(); closeErr == nil {
				closeErr = err
			}
		}
	})
	return closeErr
}

func (t *systemJenkinsCallbackTunnel) serve() {
	for {
		remoteConnection, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.closed:
				return
			default:
				t.reportError(errors.New("JENKINS_CALLBACK_TUNNEL_UNAVAILABLE"))
				return
			}
		}
		go t.proxy(remoteConnection)
	}
}

func (t *systemJenkinsCallbackTunnel) proxy(remoteConnection net.Conn) {
	defer func() {
		_ = remoteConnection.Close()
	}()
	localConnection, err := net.Dial("tcp", t.localAPIAddress)
	if err != nil {
		t.reportError(errors.New("JENKINS_CALLBACK_TUNNEL_UNAVAILABLE"))
		return
	}
	defer func() {
		_ = localConnection.Close()
	}()
	done := make(chan struct{}, 2)
	copyConnection := func(destination io.Writer, source io.Reader) {
		_, _ = io.Copy(destination, source)
		done <- struct{}{}
	}
	go copyConnection(localConnection, remoteConnection)
	go copyConnection(remoteConnection, localConnection)
	<-done
}

func (t *systemJenkinsCallbackTunnel) reportError(err error) {
	select {
	case <-t.closed:
		return
	case t.errors <- err:
	default:
	}
}

func (t *systemJenkinsCallbackTunnel) Validate(ctx context.Context, runID string, runToken string) error {
	if !remoteJenkinsReady(ctx, t.client, t.jenkinsContainer) {
		return errors.New("JENKINS_AUTHENTICATED_HEALTHCHECK_FAILED")
	}
	return nil
}

func (t *systemJenkinsCallbackTunnel) Trigger(ctx context.Context, input JenkinsReleaseTriggerInput) (domain.Object, error) {
	payload, err := json.Marshal(domain.Object{"runId": input.RunID, "materials": input.Materials, "expiresAt": input.ExpiresAt, "callback": domain.Object{"url": input.ControlPlaneBaseURL + "/platform/jenkins/release-artifacts", "token": jenkinsPurposeToken(input.RunToken, "callback")}})
	if err != nil {
		return nil, err
	}
	output, err := remoteOutputWithInput(ctx, t.client, jenkinsBrokerCommand(t.jenkinsContainer, "register", ""), payload)
	if err != nil {
		return nil, errors.New(stableJenkinsReleaseError(err))
	}
	var response domain.Object
	if json.Unmarshal([]byte(output), &response) != nil {
		return nil, errors.New("JENKINS_HANDOFF_RESPONSE_INVALID")
	}
	return response, nil
}
func (t *systemJenkinsCallbackTunnel) Start(ctx context.Context, runID string) (domain.Object, error) {
	output, err := remoteOutput(ctx, t.client, jenkinsBrokerCommand(t.jenkinsContainer, "trigger", runID))
	if err != nil {
		return nil, errors.New("JENKINS_HANDOFF_FAILED")
	}
	return decodeJenkinsBrokerObject(output)
}
func (t *systemJenkinsCallbackTunnel) Cancel(ctx context.Context, trigger domain.Object) error {
	runID := stringValue(trigger["runId"])
	if runID == "" {
		return errors.New("JENKINS_RUN_ID_REQUIRED")
	}
	output, err := remoteOutput(ctx, t.client, jenkinsBrokerCommand(t.jenkinsContainer, "cancel", runID))
	if err != nil {
		// Broker 重启恢复时可能已经清理了没有任何材料的旧运行；远端不存在运行即表示无需继续清理。
		if strings.Contains(err.Error(), "JENKINS_RUN_NOT_FOUND") {
			return nil
		}
		return errors.New("JENKINS_RELEASE_CANCEL_FAILED")
	}
	return validateJenkinsCancellation(output, runID)
}

func validateJenkinsCancellation(output, runID string) error {
	var response domain.Object
	if json.Unmarshal([]byte(output), &response) != nil || stringValue(response["runId"]) != runID {
		return errors.New("JENKINS_BROKER_RESPONSE_INVALID")
	}
	if !jenkinsRunTerminal(response) {
		return errors.New("JENKINS_CLEANUP_REQUIRED")
	}
	return nil
}
func (t *systemJenkinsCallbackTunnel) Status(ctx context.Context, runID string) (domain.Object, error) {
	output, err := remoteOutput(ctx, t.client, jenkinsBrokerCommand(t.jenkinsContainer, "status", runID))
	if err != nil {
		if strings.Contains(err.Error(), "JENKINS_RUN_NOT_FOUND") {
			return nil, errors.New("JENKINS_RUN_NOT_FOUND")
		}
		return nil, errors.New("JENKINS_BROKER_UNAVAILABLE")
	}
	var response domain.Object
	if json.Unmarshal([]byte(output), &response) != nil {
		return nil, errors.New("JENKINS_BROKER_RESPONSE_INVALID")
	}
	return response, nil
}

func remoteOutputWithInput(ctx context.Context, client *ssh.Client, command string, input []byte) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer func() {
		_ = session.Close()
	}()
	session.Stdin = bytes.NewReader(input)
	type commandResult struct {
		output []byte
		err    error
	}
	done := make(chan commandResult, 1)
	go func() {
		output, err := session.CombinedOutput(command)
		done <- commandResult{output: output, err: err}
	}()
	select {
	case result := <-done:
		output := strings.TrimSpace(string(result.output))
		if result.err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", errors.New(stableJenkinsReleaseCode(output))
		}
		return output, nil
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		return "", ctx.Err()
	}
}

func listenerPort(address net.Addr) (int, error) {
	if tcpAddress, ok := address.(*net.TCPAddr); ok && tcpAddress.Port > 0 {
		return tcpAddress.Port, nil
	}
	_, portText, err := net.SplitHostPort(address.String())
	if err != nil {
		return 0, errors.New("JENKINS_CALLBACK_TUNNEL_ADDRESS_INVALID")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 {
		return 0, errors.New("JENKINS_CALLBACK_TUNNEL_ADDRESS_INVALID")
	}
	return port, nil
}

func positiveRuntimePort(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

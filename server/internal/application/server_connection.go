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

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/serverconnection"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

type ServerConnectionInput struct {
	Mode        string `json:"mode"`
	URL         string `json:"url,omitempty"`
	Credentials struct {
		Action   string `json:"action"`
		Username string `json:"username,omitempty"`
		Password string `json:"password,omitempty"`
	} `json:"credentials"`
}

type proxyMaterial struct {
	serverconnection.Credentials
	Endpoint string `json:"endpoint"`
}

type connectionMaterialKey struct{}

func validateArchivedServerConnection(server domain.Object) error {
	raw, ok := server["connectionProxy"]
	if !ok {
		return nil
	}
	profile := objectMapFromAny(raw)
	for field := range profile {
		switch field {
		case "mode", "url", "hasCredentials":
		default:
			return errors.New("SERVER_PROXY_SETTINGS_INVALID")
		}
	}
	if flag, exists := profile["hasCredentials"]; exists {
		if _, ok := flag.(bool); !ok {
			return errors.New("SERVER_PROXY_SETTINGS_INVALID")
		}
	}
	if profile["mode"] == "direct" && profile["url"] == nil && profile["hasCredentials"] != true {
		return nil
	}
	if profile["mode"] != "manual" {
		return errors.New("SERVER_PROXY_SETTINGS_INVALID")
	}
	_, err := serverconnection.Endpoint(stringValue(profile["url"]))
	return err
}

type connectionMaterialReader interface {
	GetSecretMaterial(context.Context, string) (string, error)
}

func (r *Runtime) WithServerConnections(ctx context.Context) context.Context {
	return context.WithValue(ctx, connectionMaterialKey{}, connectionMaterialReader(r.Store))
}

func proxyMaterialRef(id string) string { return "server-proxy:" + id }

func (r *Runtime) SaveServerConnection(ctx context.Context, id string, input ServerConnectionInput) (domain.Object, error) {
	settings := domain.Object{"mode": input.Mode}
	materials := map[string]string{}
	var deletes []string
	endpoint := ""
	switch input.Mode {
	case "manual":
		u, err := serverconnection.Endpoint(input.URL)
		if err != nil {
			return nil, err
		}
		endpoint = u.String()
		settings["url"] = endpoint
	case "direct":
		if input.URL != "" || input.Credentials.Action == "replace" {
			return nil, errors.New("SERVER_PROXY_SETTINGS_INVALID")
		}
	default:
		return nil, errors.New("SERVER_PROXY_SETTINGS_INVALID")
	}
	action := input.Credentials.Action
	if action != "replace" && (input.Credentials.Username != "" || input.Credentials.Password != "") {
		return nil, errors.New("SERVER_PROXY_CREDENTIALS_INVALID")
	}
	switch action {
	case "replace":
		u, p := input.Credentials.Username, input.Credentials.Password
		if u == "" || p == "" || len(u) > 255 || len(p) > 255 || strings.ContainsAny(u, ":\r\n\x00") || strings.ContainsAny(p, "\r\n\x00") {
			return nil, errors.New("SERVER_PROXY_CREDENTIALS_INVALID")
		}
		data, _ := json.Marshal(proxyMaterial{Endpoint: endpoint, Credentials: serverconnection.Credentials{Username: u, Password: p}})
		materials[proxyMaterialRef(id)] = string(data)
		settings["hasCredentials"] = true
	case "clear", "keep":
		if action == "clear" || input.Mode == "direct" {
			deletes = append(deletes, proxyMaterialRef(id))
		}
	default:
		return nil, errors.New("SERVER_PROXY_CREDENTIALS_INVALID")
	}
	err := r.Store.MutateStateWithSecretChanges(ctx, materials, deletes, func(state *domain.State) error {
		server := findObject(state.Servers, id)
		if server == nil {
			return errors.New("SERVER_NOT_FOUND")
		}
		old := objectMapFromAny(server["connectionProxy"])
		if input.Mode == "manual" && action == "keep" && old["hasCredentials"] == true {
			if old["url"] != endpoint {
				return errors.New("SERVER_PROXY_NEW_CREDENTIALS_REQUIRED")
			}
			settings["hasCredentials"] = true
		}
		server["connectionProxy"] = settings
		server["updatedAt"] = domain.Now()
		return nil
	})
	return settings, err
}

func connectionProxy(ctx context.Context, server domain.Object) (*url.URL, *serverconnection.Credentials, error) {
	settings := objectMapFromAny(server["connectionProxy"])
	if settings["mode"] == "direct" {
		return nil, nil, nil
	}
	if settings["mode"] != "manual" {
		return nil, nil, errors.New("SERVER_PROXY_SETTINGS_INVALID")
	}
	u, err := serverconnection.Endpoint(stringValue(settings["url"]))
	if err != nil || settings["hasCredentials"] != true {
		return u, nil, err
	}
	reader, ok := ctx.Value(connectionMaterialKey{}).(connectionMaterialReader)
	if !ok {
		return nil, nil, errors.New("SERVER_PROXY_CREDENTIALS_REQUIRED")
	}
	material, err := reader.GetSecretMaterial(ctx, proxyMaterialRef(stringValue(server["id"])))
	var value proxyMaterial
	if err != nil || json.Unmarshal([]byte(material), &value) != nil || value.Endpoint != u.String() || value.Username == "" || value.Password == "" {
		return nil, nil, errors.New("SERVER_PROXY_CREDENTIALS_REQUIRED")
	}
	return u, &value.Credentials, nil
}

func openServerSSH(ctx context.Context, server domain.Object, signer ssh.Signer, requirePin bool, inspect *string) (*ssh.Client, error) {
	pin := stringValue(server["sshHostFingerprint"])
	if inspect == nil && pin == "" && (requirePin || objectMapFromAny(server["connectionProxy"])["mode"] == "manual") {
		return nil, errors.New("SSH_HOST_KEY_TRUST_REQUIRED")
	}
	host := serverPublicHost(server)
	if host == "" {
		return nil, errors.New("SERVER_PUBLIC_HOST_REQUIRED")
	}
	address := net.JoinHostPort(host, strconv.Itoa(numberOrDefault(server["port"], 22)))
	u, credentials, err := connectionProxy(ctx, server)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := serverconnection.Dial(dialCtx, address, u, credentials)
	if err != nil {
		if u != nil {
			return nil, err
		}
		return nil, errors.New("SSH_CONNECT_FAILED")
	}
	stopCancel := context.AfterFunc(dialCtx, func() { _ = conn.Close() })
	defer stopCancel()
	deadline, _ := dialCtx.Deadline()
	_ = conn.SetDeadline(deadline)
	var hostErr error
	config := &ssh.ClientConfig{User: firstNonEmptyString(stringValue(server["sshUser"]), "root"), HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fingerprint := ssh.FingerprintSHA256(key)
		if inspect != nil {
			*inspect = fingerprint
			return errors.New("SSH_HOST_KEY_OBSERVED")
		}
		if pin != "" && pin != fingerprint {
			hostErr = errors.New("SSH_HOST_KEY_CHANGED")
		}
		return hostErr
	}}
	if signer != nil {
		config.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	}
	sshConn, chans, requests, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		_ = conn.Close()
		if hostErr != nil {
			return nil, hostErr
		}
		if inspect != nil && *inspect != "" {
			return nil, nil
		}
		return nil, errors.New("SSH_HANDSHAKE_FAILED")
	}
	if !stopCancel() {
		_ = sshConn.Close()
		return nil, dialCtx.Err()
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, chans, requests), nil
}

func (r *Runtime) openTerminalSSH(ctx context.Context, server domain.Object, privateKey string) (*ssh.Client, error) {
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return nil, errors.New("SSH_PRIVATE_KEY_INVALID")
	}
	return openServerSSH(r.WithServerConnections(ctx), server, signer, true, nil)
}

func (r *Runtime) CheckServerConnection(ctx context.Context, id string, expected *string) (domain.Object, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	server := findObject(state.Servers, id)
	if server == nil {
		return nil, errors.New("SERVER_NOT_FOUND")
	}
	observed := ""
	_, err = openServerSSH(r.WithServerConnections(ctx), server, nil, false, &observed)
	if err != nil {
		return nil, err
	}
	old := stringValue(server["sshHostFingerprint"])
	if expected != nil {
		if observed != *expected || observed == "" {
			return nil, errors.New("SSH_HOST_KEY_CHANGED")
		}
		err = r.Store.MutateState(ctx, func(state *domain.State) error {
			current := findObject(state.Servers, id)
			if current == nil {
				return errors.New("SERVER_NOT_FOUND")
			}
			cfg := objectMapFromAny(server["connectionProxy"])
			now := objectMapFromAny(current["connectionProxy"])
			if stringValue(current["sshHostFingerprint"]) != old || current["publicHost"] != server["publicHost"] || current["port"] != server["port"] || cfg["url"] != now["url"] || cfg["mode"] != now["mode"] {
				return errors.New("SERVER_CONNECTION_CHANGED")
			}
			current["sshHostFingerprint"] = observed
			current["updatedAt"] = domain.Now()
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return domain.Object{"fingerprint": observed, "trusted": old == observed || expected != nil, "previousFingerprint": old}, nil
}

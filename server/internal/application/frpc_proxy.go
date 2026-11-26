package application

import (
	"context"
	"net/url"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) frpcProxyURL(ctx context.Context, server domain.Object) (string, error) {
	endpoint, credentials, err := connectionProxy(r.WithServerConnections(ctx), server)
	if err != nil || endpoint == nil {
		return "", err
	}
	if credentials != nil {
		endpoint.User = url.UserPassword(credentials.Username, credentials.Password)
	}
	return endpoint.String(), nil
}

// frpc 会自动读取 http_proxy；从子进程环境清除继承值，再应用目标服务器的设置。
func frpcEnvironment(inherited []string, input FrpTunnelExecutionInput) []string {
	env := make([]string, 0, len(inherited)+2)
	for _, entry := range inherited {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy", "frp_token":
			continue
		}
		env = append(env, entry)
	}
	if input.proxyURL != "" {
		env = append(env, "http_proxy="+input.proxyURL)
	}
	return append(env, "FRP_TOKEN="+input.Token)
}

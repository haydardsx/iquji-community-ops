package domain

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// DockerRegistryMirrorURL 校验可选的镜像地址并返回规范形式。
func DockerRegistryMirrorURL(value any) (string, bool) {
	if value == nil {
		return "", true
	}
	raw, ok := value.(string)
	if !ok {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || strings.ContainsAny(raw, "?#\\") || strings.IndexFunc(raw, unicode.IsSpace) >= 0 || strings.HasSuffix(parsed.Host, ":") {
		return "", false
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", false
		}
	}
	parsed.Host = strings.ToLower(parsed.Host)
	return strings.TrimRight(parsed.String(), "/"), true
}

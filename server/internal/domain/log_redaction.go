package domain

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var applicationLogPrivateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
var applicationLogPrivateKeyBegin = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
var applicationLogPrivateKeyEnd = regexp.MustCompile(`-----END [A-Z ]*PRIVATE KEY-----`)
var applicationLogBearer = regexp.MustCompile(`(?i)\bBearer\s+[a-z0-9._~+/=-]+`)
var sensitiveLogQueryKey = regexp.MustCompile(`(?i)(?:password|passwd|pwd|token|secret|api[_-]?key|authorization|signature)$`)
var applicationLogCredentials = regexp.MustCompile(`(?i)((?:"?(?:password|passwd|pwd|token|secret|api[_-]?key|authorization|signature)"?)\s*[:=]\s*)("[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)`)
var applicationLogURLCredentials = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@]+@`)

func RedactLogText(value string, secrets []string) string {
	// 先处理整段原文，再分行或截断，避免多行材料被拆开后遗漏。
	value = applicationLogPrivateKey.ReplaceAllString(value, "[REDACTED]")
	for end := applicationLogPrivateKeyEnd.FindStringIndex(value); end != nil; end = applicationLogPrivateKeyEnd.FindStringIndex(value) {
		value = "[REDACTED]" + value[end[1]:]
	}
	if begin := applicationLogPrivateKeyBegin.FindStringIndex(value); begin != nil {
		value = value[:begin[0]] + "[REDACTED]"
	}
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
			if strings.Contains(secret, "PRIVATE KEY-----") {
				// Docker 为每行添加时间戳后，完整 PEM 不再是连续子串，逐行遮蔽材料。
				for _, line := range strings.Split(secret, "\n") {
					if line = strings.TrimSpace(line); line != "" {
						value = strings.ReplaceAll(value, line, "[REDACTED]")
					}
				}
			}
		}
	}
	value = applicationLogBearer.ReplaceAllString(value, "Bearer [REDACTED]")
	value = applicationLogURLCredentials.ReplaceAllString(value, "${1}[REDACTED]@")
	value = applicationLogCredentials.ReplaceAllString(value, "${1}[REDACTED]")
	return strings.ToValidUTF8(value, "�")
}

// LogSummary 将自有日志压为有界单行；外部进程原文只使用 RedactLogText。
func LogSummary(message string) string {
	message = strings.Join(strings.Fields(RedactLogText(message, nil)), " ")
	return boundedLogText(message)
}

func boundedLogText(message string) string {
	const maxBytes = 600
	if len(message) <= maxBytes {
		return message
	}
	end := maxBytes - 3
	for !utf8.RuneStart(message[end]) {
		end--
	}
	return message[:end] + "..."
}

// NormalizeLog 用同一访问事件格式展示历史与新命中，写入和恢复同样执行脱敏。
func (hit *FrpProbeHit) NormalizeLog() {
	hit.Source, hit.Event, hit.Level = "probe", "probe.request", "info"
	hit.Timestamp, _, _ = normalizedLogFields(hit.ReceivedAt, "info", "")
	hit.Method, hit.Host, hit.Path = LogSummary(hit.Method), LogSummary(hit.Host), LogSummary(hit.Path)
	hit.RemoteAddr, hit.UserAgent = LogSummary(hit.RemoteAddr), LogSummary(hit.UserAgent)
	hit.Message = LogSummary(hit.Method + " " + hit.Path)
	if hit.RawQuery == "" {
		return
	}
	query, err := url.ParseQuery(hit.RawQuery)
	if err != nil {
		hit.RawQuery = "[REDACTED]"
		return
	}
	for key, values := range query {
		sensitive := sensitiveLogQueryKey.MatchString(key)
		for i, value := range values {
			if sensitive {
				values[i] = "[REDACTED]"
			} else {
				values[i] = RedactLogText(value, nil)
			}
		}
	}
	hit.RawQuery = boundedLogText(query.Encode())
}

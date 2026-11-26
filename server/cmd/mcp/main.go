// iquops-mcp 通过标准输入输出服务 MCP 客户端，也支持单次脚本调用。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/automation"
)

func main() {
	if err := run(); err != nil {
		if len(os.Args) > 1 {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"errorCode": err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
func run() error {
	ctx := context.Background()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "list":
			if os.Getenv("IQUOPS_MCP_TOKEN") != "" {
				directory, err := requestDesktop(ctx, "discovery", nil)
				if err != nil {
					return err
				}
				return json.NewEncoder(os.Stdout).Encode(directory)
			}
			return json.NewEncoder(os.Stdout).Encode(automation.Directory())
		case "call":
			if len(os.Args) < 3 || len(os.Args) > 4 {
				return errors.New("用法：iquops-mcp call <命令> [JSON 参数]")
			}
			c := automation.Call{Name: os.Args[2]}
			if len(os.Args) == 4 {
				if err := json.Unmarshal([]byte(os.Args[3]), &c.Args); err != nil {
					return errors.New("参数必须是 JSON 字符串对象")
				}
			}
			v, e := invoke(ctx, c)
			if e != nil {
				return e
			}
			return json.NewEncoder(os.Stdout).Encode(v)
		default:
			return errors.New("可用子命令为 list、call。无参数时启动 stdio MCP")
		}
	}
	return serve(ctx)
}
func invoke(ctx context.Context, c automation.Call) (json.RawMessage, error) {
	if _, err := c.Validate(); err != nil {
		return nil, err
	}
	return requestDesktop(ctx, "call", c)
}
func requestDesktop(ctx context.Context, endpoint string, body any) (json.RawMessage, error) {
	token := os.Getenv("IQUOPS_MCP_TOKEN")
	if token == "" {
		return nil, errors.New("AUTOMATION_CLIENT_UNAUTHORIZED")
	}
	dir := os.Getenv("IQUOPS_STATE_DIR")
	if dir == "" {
		root, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(root, "IQuOps", "state")
	}
	data, err := os.ReadFile(filepath.Join(dir, "automation.port"))
	if err != nil {
		return nil, errors.New("AUTOMATION_DESKTOP_OFFLINE")
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("AUTOMATION_DESKTOP_OFFLINE")
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(port)+"/api/automation/"+endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 110 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("AUTOMATION_DESKTOP_OFFLINE")
	}
	defer res.Body.Close()
	data, err = io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil || !json.Valid(data) {
		return nil, errors.New("AUTOMATION_RESPONSE_INVALID")
	}
	if res.StatusCode/100 != 2 {
		var problem struct{ ErrorCode string }
		_ = json.Unmarshal(data, &problem)
		if problem.ErrorCode == "" {
			return nil, errors.New("AUTOMATION_CALL_FAILED")
		}
		return nil, errors.New(problem.ErrorCode)
	}
	return data, nil
}

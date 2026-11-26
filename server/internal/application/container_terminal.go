package application

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Docker 独立持有 exec 的 PTY。Shell 进入交互前先报告内核身份，
// 关闭时核对 PID、会话和启动时间，避免向复用 PID 的进程发送信号。
const containerTerminalStart = `set -f
IFS= read -r stat < "/proc/$$/stat" || exit 125
set -- ${stat##*) }
[ "$4" = "$$" ] && [ "$5" != 0 ] || exit 125
read_ack() {
  if printf '\n' | (IFS= read -r -t 1 ack) 2>/dev/null; then
    IFS= read -r -t 15 ack && [ -z "$ack" ]
  else
    saved=$(stty -g) && stty -icanon -echo min 0 time 150 || return 1
    IFS= read -r ack && [ -z "$ack" ] && stty "$saved"
  fi
}
# 回收通道就绪之前不启动交互。断线时等待确认的 Shell 自行退出。
printf 'IQuOps-PTY %s %s\n' "$$" "${20}"
read_ack || exit 125
exec "$0" -i`

const containerTerminalStop = `set -f
pid=$1
start=$2
check_leader() {
  IFS= read -r stat 2>/dev/null < "/proc/$pid/stat" || return 1
  set -- ${stat##*) }
  [ "$4" = "$pid" ] && [ "${20}" = "$start" ] || return 1
  foreground=$6
}
check_leader || exit 0
# 先让前台作业处理挂断信号并由 Shell 回收。强制终止前再次核对会话身份。
if [ "$foreground" -gt 1 ] && [ "$foreground" != "$pid" ]; then
  job=$foreground
  kill -HUP "-$job" 2>/dev/null || :
  for delay in 1 2 3 4 5; do
    check_leader || exit 0
    [ "$foreground" = "$job" ] || break
    sleep 0.1
  done
  if [ "$foreground" = "$job" ]; then
    kill -KILL "-$job" 2>/dev/null || :
    sleep 0.1
  fi
fi
check_leader || exit 0
kill -HUP "$pid" 2>/dev/null || exit 0
sleep 0.1
check_leader && kill -KILL "$pid" 2>/dev/null || :`

var terminalLeaderRecord = regexp.MustCompile(`^IQuOps-PTY ([1-9][0-9]{0,9}) ([0-9]{1,20})$`)

type terminalLeader struct{ pid, start string }

// SSH 可能分段交付首行，也可能把首个提示符与首行放进同一帧。
type containerTerminalOutput struct {
	mu     sync.Mutex
	leader chan terminalLeader
	line   []byte
	ready  bool
	output io.Writer
}

func (w *containerTerminalOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ready {
		return w.output.Write(data)
	}
	n := bytes.IndexByte(data, '\n')
	if n < 0 {
		if len(w.line)+len(data) > 64 {
			return 0, errors.New("TERMINAL_SESSION_FAILED")
		}
		w.line = append(w.line, data...)
		return len(data), nil
	}
	if n+len(w.line) > 64 {
		return 0, errors.New("TERMINAL_SESSION_FAILED")
	}
	w.line = append(w.line, data[:n]...)
	record := terminalLeaderRecord.FindStringSubmatch(strings.TrimSuffix(string(w.line), "\r"))
	if record == nil || record[1] == "1" {
		return 0, errors.New("TERMINAL_SESSION_FAILED")
	}
	w.leader <- terminalLeader{record[1], record[2]}
	w.ready = true
	w.line = nil
	if n+1 < len(data) {
		if _, err := w.output.Write(data[n+1:]); err != nil {
			return n + 1, err
		}
	}
	return len(data), nil
}

type containerTerminal struct{ id, shell string }

func (c containerTerminal) startCommand() string {
	return "exec docker exec -it " + c.id + " " + c.shell + " -c " + shellQuote(containerTerminalStart) + " " + c.shell
}

// 独立 SSH 通道不申请 PTY，sshd 在传输中断时关闭其标准输入。
// 回收命令已经在目标节点就绪，Ops 进程退出也无需重新连接。
func (c containerTerminal) watch(client *ssh.Client, leader terminalLeader) (func(), error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	input, err := session.StdinPipe()
	if err == nil {
		command := "cat >/dev/null; exec docker exec " + c.id + " " + c.shell + " -c " + shellQuote(containerTerminalStop) + " -- " + leader.pid + " " + leader.start
		err = session.Start(command)
	}
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	done := make(chan struct{})
	go func() { _ = session.Wait(); close(done) }()
	return func() {
		_ = input.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		select {
		case <-done:
		case <-ctx.Done():
		}
	}, nil
}

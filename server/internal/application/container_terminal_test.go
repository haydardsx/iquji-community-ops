package application

import (
	"bytes"
	"strings"
	"testing"
)

func TestContainerTerminalConsumesSplitIdentity(t *testing.T) {
	var stream bytes.Buffer
	output := containerTerminalOutput{leader: make(chan terminalLeader, 1), output: &stream}
	for _, frame := range []string{"IQu", "Ops-PTY 72 ", "3491\r", "\n$ ", "text"} {
		if n, err := output.Write([]byte(frame)); n != len(frame) || err != nil {
			t.Fatalf("首行分段后无法继续读取：%d %v", n, err)
		}
	}
	if id := <-output.leader; id.pid != "72" || id.start != "3491" || stream.String() != "$ text" {
		t.Fatal("握手未与终端正文分开")
	}
	for _, record := range []string{"IQuOps-PTY 1 3491\n", "IQuOps-PTY -72 3491\n", "IQuOps-PTY 72\n", strings.Repeat("x", 65)} {
		invalid := containerTerminalOutput{leader: make(chan terminalLeader, 1), output: &stream}
		if _, err := invalid.Write([]byte(record)); err == nil || len(invalid.leader) != 0 {
			t.Fatal("接受了无效进程身份")
		}
	}
}

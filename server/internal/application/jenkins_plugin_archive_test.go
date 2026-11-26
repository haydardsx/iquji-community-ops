package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jenkinsArchiveFixture(t *testing.T, names ...string) ([]byte, []byte) {
	t.Helper()
	var data bytes.Buffer
	writer := tar.NewWriter(&data)
	content := []byte("plugin")
	for _, name := range names {
		header := &tar.Header{Name: name, Mode: 0644, Size: int64(len(content)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes(), fmt.Appendf(nil, "sample:1.0:%x\n", sha256.Sum256(content))
}

func TestJenkinsPluginArchiveMustMatchLock(t *testing.T) {
	valid, lock := jenkinsArchiveFixture(t, "sample.jpi")
	missing, _ := jenkinsArchiveFixture(t)
	extra, _ := jenkinsArchiveFixture(t, "sample.jpi", "other.jpi")
	duplicate, _ := jenkinsArchiveFixture(t, "sample.jpi", "sample.jpi")
	unsafe, _ := jenkinsArchiveFixture(t, "../sample.jpi")
	modified := bytes.Replace(valid, []byte("plugin"), []byte("broken"), 1)
	if err := verifyJenkinsPluginArchive(bytes.NewReader(valid), lock); err != nil {
		t.Fatal(err)
	}
	for _, content := range [][]byte{missing, extra, duplicate, unsafe, modified, append(bytes.Clone(valid), valid...)} {
		if err := verifyJenkinsPluginArchive(bytes.NewReader(content), lock); !errors.Is(err, errJenkinsPluginBundle) {
			t.Fatal("不完整、未声明或摘要不符的插件归档通过了校验")
		}
	}
}

func TestInstallJenkinsVerifiesAndStreamsPluginsBeforeBuilding(t *testing.T) {
	src := filepath.Join("..", "..", "resources", "runtime", "managed", "jenkins")
	bundle := t.TempDir()
	if err := os.CopyFS(bundle, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	input := ServerRuntimeExecutionInput{JenkinsBundleDir: bundle}
	executor := NewSystemServerRuntimeExecutor()
	if _, err := executor.InstallJenkins(context.Background(), input); !errors.Is(err, errJenkinsPluginBundle) {
		t.Fatalf("缺少插件时应在连接 SSH 前报错: %v", err)
	}
	archive, lock := jenkinsArchiveFixture(t, "sample.jpi")
	for name, data := range map[string][]byte{"jenkins-plugins.tar": archive, "plugins.lock": lock} {
		if err := os.WriteFile(filepath.Join(bundle, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var events []string
	_, server, key := runtimeProbeSSHWithInput(t, func(command string, stdin io.Reader) (string, uint32) {
		switch {
		case strings.Contains(command, "mktemp") && strings.Contains(command, "jenkins-plugins.tar"):
			data, err := io.ReadAll(stdin)
			if err != nil || !bytes.Equal(data, archive) || !strings.Contains(command, fmt.Sprintf("%x", sha256.Sum256(archive))) {
				return "上传内容或摘要不符", 1
			}
			events = append(events, "plugins")
		case strings.Contains(command, "docker build --target"):
			events = append(events, "build")
			if strings.Contains(command, "'controller'") {
				return "build step failed", 1
			}
		case strings.Contains(command, "/control.json"):
			events = append(events, "credentials")
		case strings.Contains(command, "cat >"):
			_, _ = io.Copy(io.Discard, stdin)
		}
		return "", 0
	})
	input.Server, input.SSHKey = server, key
	if _, err := executor.InstallJenkins(context.Background(), input); err == nil || err.Error() != "JENKINS_IMAGE_BUILD_FAILED" {
		t.Fatalf("应观测到测试控制器的构建失败: %v", err)
	}
	if got := strings.Join(events, ","); got != "plugins,build,build" {
		t.Fatalf("插件上传和构建失败时的凭据处理顺序有误: %s", got)
	}
}

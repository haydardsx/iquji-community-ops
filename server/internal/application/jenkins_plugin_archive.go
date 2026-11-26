package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
	"golang.org/x/crypto/ssh"
)

var (
	errJenkinsPluginBundle = errors.New("JENKINS_PLUGIN_BUNDLE_INVALID")
	jenkinsPluginID        = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
)

// 在连接服务器前核验每个插件；整包摘要用于远端原子替换前的传输校验。
func openJenkinsPluginArchive(input ServerRuntimeExecutionInput, bundle []jenkinsBundleFile) (*os.File, string, error) {
	var lock []byte
	for _, resource := range bundle {
		if resource.RelativePath == "plugins.lock" {
			lock = resource.Content
			break
		}
	}
	path := input.JenkinsPluginsArchive
	if path == "" {
		path = filepath.Join(input.JenkinsBundleDir, platform.ManagedJenkinsPluginArchive)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", errJenkinsPluginBundle
	}
	info, err := file.Stat()
	if err == nil && info.Mode().IsRegular() {
		hash := sha256.New()
		if verifyJenkinsPluginArchive(io.TeeReader(file, hash), lock) == nil {
			if _, err := file.Seek(0, io.SeekStart); err == nil {
				return file, hex.EncodeToString(hash.Sum(nil)), nil
			}
		}
	}
	_ = file.Close()
	return nil, "", errJenkinsPluginBundle
}

func verifyJenkinsPluginArchive(input io.Reader, lock []byte) error {
	expected := map[string]string{}
	for line := range strings.Lines(strings.TrimSpace(string(lock))) {
		entry := strings.Split(strings.TrimSpace(line), ":")
		if len(entry) != 3 || !jenkinsPluginID.MatchString(entry[0]) || entry[1] == "" || len(entry[2]) != sha256.Size*2 {
			return errJenkinsPluginBundle
		}
		name := entry[0] + ".jpi"
		if expected[name] != "" {
			return errJenkinsPluginBundle
		}
		expected[name] = entry[2]
	}
	if len(expected) == 0 {
		return errJenkinsPluginBundle
	}
	reader := tar.NewReader(input)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil || header.Format != tar.FormatUSTAR || header.Typeflag != tar.TypeReg || header.Mode != 0644 || expected[header.Name] == "" {
			return errJenkinsPluginBundle
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, reader); err != nil || hex.EncodeToString(hash.Sum(nil)) != expected[header.Name] {
			return errJenkinsPluginBundle
		}
		delete(expected, header.Name)
	}
	if len(expected) != 0 {
		return errJenkinsPluginBundle
	}
	// tar 允许结尾使用零字节对齐，禁止在结尾追加第二段未核验的归档。
	var tail [4096]byte
	for {
		n, err := io.ReadFull(input, tail[:])
		if len(bytes.Trim(tail[:n], "\x00")) != 0 {
			return errJenkinsPluginBundle
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return errJenkinsPluginBundle
		}
	}
}

func uploadJenkinsPluginArchive(ctx context.Context, client *ssh.Client, root string, archive *os.File, digest string) error {
	destination := shellQuote(root + "/" + platform.ManagedJenkinsPluginArchive)
	command := "set -eu\numask 077\ntmp=$(mktemp " + destination + ".XXXXXXXX)\n" +
		"trap 'rm -f \"$tmp\"' EXIT\ncat > \"$tmp\"\n" +
		"printf '%s  %s\\n' " + shellQuote(digest) + " \"$tmp\" | sha256sum --strict -c - >/dev/null\n" +
		"mv -f \"$tmp\" " + destination
	return writeRemoteStream(ctx, client, command, archive)
}

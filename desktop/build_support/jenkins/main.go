package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type plugin struct {
	id, version, digest string
}

var lockEntry = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_-]*):([A-Za-z0-9][A-Za-z0-9._+-]*):([a-f0-9]{64})$`)

func readLock(filename string) ([]plugin, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var entries []plugin
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := lockEntry.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if fields == nil || seen[fields[1]] {
			return nil, fmt.Errorf("插件锁文件第 %d 行无效或重复", i+1)
		}
		seen[fields[1]] = true
		entries = append(entries, plugin{fields[1], fields[2], fields[3]})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	return entries, nil
}

func (p plugin) download(cache string) ([]byte, error) {
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return nil, err
	}
	filename := filepath.Join(cache, p.digest+".jpi")
	if data, err := os.ReadFile(filename); err == nil && p.matches(data) {
		return data, nil
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	suffix := p.id + "/" + p.version + "/" + p.id + ".hpi"
	var failures []error
	for _, host := range []string{"https://updates.jenkins.io/download/plugins/", "https://archives.jenkins.io/plugins/"} {
		data, err := p.fetch(client, host+suffix)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if err := replaceFile(filename, 0o600, func(f *os.File) error {
			_, err := f.Write(data)
			return err
		}); err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, errors.Join(failures...)
}

func (p plugin) fetch(client *http.Client, url string) ([]byte, error) {
	response, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 %s 返回 HTTP %d", url, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 128<<20))
	if err != nil {
		return nil, err
	}
	if !p.matches(data) {
		return nil, fmt.Errorf("%s 的 SHA-256 与锁文件不符", url)
	}
	return data, nil
}

func (p plugin) matches(data []byte) bool {
	return fmt.Sprintf("%x", sha256.Sum256(data)) == p.digest
}

func (p plugin) contents(offlineDir, cache string) ([]byte, error) {
	if offlineDir == "" {
		return p.download(cache)
	}
	data, err := os.ReadFile(filepath.Join(offlineDir, p.id+".jpi"))
	if err != nil {
		return nil, err
	}
	if !p.matches(data) {
		return nil, fmt.Errorf("本地插件 %s 的 SHA-256 与锁文件不符", p.id)
	}
	return data, nil
}

func (p plugin) header(size int64) *tar.Header {
	return &tar.Header{Name: p.id + ".jpi", Size: size, Mode: 0o644, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
}

func replaceFile(filename string, mode os.FileMode, write func(*os.File) error) error {
	f, err := os.CreateTemp(filepath.Dir(filename), ".jenkins-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := write(f); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filename)
}

// 将已有归档与按同一规则重建的字节流比对，连同头部、顺序、填充和结束块一起校验。
func verifiedArchive(filename string, entries []plugin) bool {
	f, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer f.Close()
	actual := sha256.New()
	input := io.TeeReader(f, actual)
	reader := tar.NewReader(input)
	expected := sha256.New()
	writer := tar.NewWriter(expected)
	for _, p := range entries {
		header, err := reader.Next()
		if err != nil || header.Name != p.id+".jpi" || header.Typeflag != tar.TypeReg {
			return false
		}
		if err := writer.WriteHeader(p.header(header.Size)); err != nil {
			return false
		}
		digest := sha256.New()
		if _, err := io.Copy(io.MultiWriter(writer, digest), reader); err != nil || fmt.Sprintf("%x", digest.Sum(nil)) != p.digest {
			return false
		}
	}
	if _, err := io.Copy(io.Discard, input); err != nil || writer.Close() != nil {
		return false
	}
	return bytes.Equal(actual.Sum(nil), expected.Sum(nil))
}

func bundle(lock, filename, cache, offlineDir string) error {
	entries, err := readLock(lock)
	if err != nil {
		return err
	}
	if offlineDir == "" && verifiedArchive(filename, entries) {
		return nil
	}
	return replaceFile(filename, 0o644, func(f *os.File) error {
		writer := tar.NewWriter(f)
		for _, p := range entries {
			data, err := p.contents(offlineDir, cache)
			if err != nil {
				return fmt.Errorf("%s %s：%w", p.id, p.version, err)
			}
			if err := writer.WriteHeader(p.header(int64(len(data)))); err != nil {
				return err
			}
			if _, err := writer.Write(data); err != nil {
				return err
			}
		}
		return writer.Close()
	})
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "需要指定插件锁文件、输出归档和缓存目录")
		os.Exit(1)
	}
	if err := bundle(os.Args[1], os.Args[2], os.Args[3], os.Getenv("IQUOPS_JENKINS_PLUGINS_DIR")); err != nil {
		fmt.Fprintln(os.Stderr, "准备 Jenkins 插件归档失败：", err)
		os.Exit(1)
	}
}

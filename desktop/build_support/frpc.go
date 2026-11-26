package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const frpVersion = "0.69.1"

// 校验值与上游 v0.69.1 发行版的 frp_sha256_checksums.txt 一致。
var releaseArchives = map[string]struct {
	format string
	digest string
}{
	"darwin_amd64":  {"tar.gz", "2bc26d02100ef333f2712149ea5997dc530dc0eefac64f4be41cb0f49d032f40"},
	"darwin_arm64":  {"tar.gz", "310012e2f1dcf3cdde2605d29b95340b686c94d1680a23711d58efeffc02f64e"},
	"linux_amd64":   {"tar.gz", "7be257b72dbbc60bcb3e0e25a5afd1dfac7b63f897084864d3c956dd3d5674e1"},
	"linux_arm64":   {"tar.gz", "bbc0c75e896af3f292fb46ba09c844a04fa9b5ea3530c039c7af20637f836355"},
	"windows_amd64": {"zip", "829ac915f8655d4d4e021b8db61b46c3445205ed80d32b04cda7fa89d87c46e0"},
	"windows_arm64": {"zip", "9b88e6eefc5d9ea2a1d5869026287e269e3d1486ac5bb08f7b4d2b26bdd6166d"},
}

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "需要指定 frpc 目标系统、目标架构、运行目录和缓存目录")
		os.Exit(1)
	}
	if err := bundleFrpc(os.Args[1], os.Args[2], os.Args[3], os.Args[4]); err != nil {
		fmt.Fprintln(os.Stderr, "准备桌面 frpc 失败：", err)
		os.Exit(1)
	}
}

func bundleFrpc(targetOS string, arch string, runtimeDir string, cacheDir string) error {
	target := targetOS + "_" + arch
	release, ok := releaseArchives[target]
	if !ok {
		return fmt.Errorf("frpc 不支持目标平台 %s", target)
	}
	baseName := "frp_" + frpVersion + "_" + target
	archiveName := baseName + "." + release.format
	data, err := verifiedArchive(filepath.Join(cacheDir, archiveName), archiveName, release.digest)
	if err != nil {
		return err
	}

	binaryName := "frpc"
	if targetOS == "windows" {
		binaryName += ".exe"
	}
	binaryMember := baseName + "/" + binaryName
	licenseMember := baseName + "/LICENSE"
	contents := map[string][]byte{binaryMember: nil, licenseMember: nil}
	if release.format == "zip" {
		err = readZIP(data, contents)
	} else {
		err = readTarGzip(data, contents)
	}
	if err != nil {
		return err
	}
	if len(contents[binaryMember]) == 0 || len(contents[licenseMember]) == 0 {
		return fmt.Errorf("归档 %s 缺少 frpc 或许可证文件", archiveName)
	}
	if err := writeIfChanged(filepath.Join(runtimeDir, "frpc-LICENSE"), contents[licenseMember], 0o644); err != nil {
		return err
	}
	return writeIfChanged(filepath.Join(runtimeDir, binaryName), contents[binaryMember], 0o755)
}

func verifiedArchive(cachePath string, name string, digest string) ([]byte, error) {
	checksumMatches := func(data []byte) bool {
		return fmt.Sprintf("%x", sha256.Sum256(data)) == digest
	}
	if archive := os.Getenv("IQUOPS_FRP_ARCHIVE"); archive != "" {
		data, err := os.ReadFile(archive)
		if err != nil {
			return nil, err
		}
		if !checksumMatches(data) {
			return nil, fmt.Errorf("frp 归档校验失败：%s", name)
		}
		return data, nil
	}
	if data, err := os.ReadFile(cachePath); err == nil && checksumMatches(data) {
		return data, nil
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	url := "https://github.com/fatedier/frp/releases/download/v" + frpVersion + "/" + name
	response, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 %s 失败，HTTP %d", name, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if !checksumMatches(data) {
		return nil, fmt.Errorf("归档 %s 的 SHA-256 校验失败", name)
	}
	if err := writeIfChanged(cachePath, data, 0o600); err != nil {
		return nil, err
	}
	return data, nil
}

func readTarGzip(data []byte, contents map[string][]byte) error {
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	for {
		entry, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if _, wanted := contents[entry.Name]; !wanted || entry.Typeflag != tar.TypeReg {
			continue
		}
		contents[entry.Name], err = io.ReadAll(reader)
		if err != nil {
			return err
		}
	}
}

func readZIP(data []byte, contents map[string][]byte) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, entry := range reader.File {
		if _, wanted := contents[entry.Name]; !wanted || !entry.Mode().IsRegular() {
			continue
		}
		file, err := entry.Open()
		if err != nil {
			return err
		}
		contents[entry.Name], err = io.ReadAll(file)
		file.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeIfChanged(path string, data []byte, mode os.FileMode) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		if runtime.GOOS == "windows" {
			return nil
		}
		return os.Chmod(path, mode)
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

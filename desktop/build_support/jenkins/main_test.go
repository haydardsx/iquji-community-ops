package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestBundleVerifiesInputsAndReusesCanonicalArchive(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sample.jpi")
	lock := filepath.Join(dir, "plugins.lock")
	archive := filepath.Join(dir, "plugins.tar")
	cache := filepath.Join(dir, "cache")
	payload := []byte("sample plugin")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	write := func(name string, content []byte) {
		t.Helper()
		if err := os.WriteFile(name, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) []byte {
		t.Helper()
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	write(lock, []byte("sample:1.0:"+digest+"\n"))
	write(file, payload)
	if err := bundle(lock, archive, cache, dir); err != nil {
		t.Fatal(err)
	}
	entries, err := readLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	canonical := read(archive)
	if !verifiedArchive(archive, entries) {
		t.Fatal("未识别规范归档")
	}
	if err := bundle(lock, archive, cache, ""); err != nil {
		t.Fatal(err)
	}
	// 额外尾部、缺失结束块及原文件被篡改，都不能复用。
	for _, bad := range [][]byte{
		append(bytes.Clone(canonical), 0),
		canonical[:len(canonical)-512],
		bytes.Replace(canonical, payload, []byte("changed file!"), 1),
	} {
		write(archive, bad)
		if verifiedArchive(archive, entries) {
			t.Fatal("复用了损坏的归档")
		}
	}
	if err := bundle(lock, archive, cache, dir); err != nil || !bytes.Equal(read(archive), canonical) {
		t.Fatalf("重建归档与原归档不一致：%v", err)
	}
	write(file, []byte("damaged"))
	if err := bundle(lock, archive, cache, dir); err == nil || !bytes.Equal(read(archive), canonical) {
		t.Fatal("本地插件损坏时未保留原归档")
	}
}

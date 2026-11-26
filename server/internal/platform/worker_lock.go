package platform

import (
	"errors"
	"os"
	"path/filepath"
)

// AcquireWorkerLock 防止同一控制面启动第二个正式 Worker；文件只保存在本机状态目录。
func AcquireWorkerLock(stateDir string) (func(), error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, errors.New("WORKER_STATE_DIRECTORY_UNAVAILABLE")
	}
	file, err := os.OpenFile(filepath.Join(stateDir, "worker.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errors.New("WORKER_LOCK_UNAVAILABLE")
	}
	unlock, err := tryWorkerFileLock(file)
	if err != nil {
		_ = file.Close()
		return nil, errors.New("WORKER_ALREADY_RUNNING")
	}
	return func() { unlock(); _ = file.Close() }, nil
}

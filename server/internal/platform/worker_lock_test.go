package platform

import "testing"

func TestWorkerLockRejectsSecondProcessUntilRelease(t *testing.T) {
	directory := t.TempDir()
	release, err := AcquireWorkerLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := AcquireWorkerLock(directory); err == nil {
		other()
		release()
		t.Fatal("同一控制面允许了第二个Worker")
	}
	release()
	release, err = AcquireWorkerLock(directory)
	if err != nil {
		t.Fatalf("释放后不能重启Worker: %v", err)
	}
	release()
}

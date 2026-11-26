package automation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// 每个客户端和提交键仅派发一次。窗口未能回传结果时保留待核对记录，避免重复创建资源。
type receipts struct {
	dir string
	mu  sync.Mutex
}

type receipt struct {
	Target string          `json:"target"`
	Result json.RawMessage `json:"result,omitempty"`
}

func (s *receipts) reserve(client string, call Call) (string, json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return "", nil, errors.New("AUTOMATION_RECEIPT_UNAVAILABLE")
	}
	path := filepath.Join(s.dir, tokenHash(client+"\n"+call.Args["requestKey"]))
	target := call.Args["target"]
	data, err := os.ReadFile(path)
	if err == nil {
		var old receipt
		if json.Unmarshal(data, &old) != nil {
			return "", nil, errors.New("AUTOMATION_RECEIPT_UNAVAILABLE")
		}
		if old.Target != target {
			return "", nil, errors.New("AUTOMATION_REQUEST_KEY_CONFLICT")
		}
		if old.Result == nil {
			return "", nil, errors.New("AUTOMATION_SUBMISSION_UNRESOLVED")
		}
		return "", old.Result, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", nil, errors.New("AUTOMATION_RECEIPT_UNAVAILABLE")
	}
	if err = s.save(path, receipt{Target: target}); err != nil {
		return "", nil, err
	}
	return path, nil, nil
}
func (s *receipts) save(path string, r receipt) error {
	data, err := json.Marshal(r)
	if err == nil {
		err = atomicSave(path, data)
	}
	if err != nil {
		return errors.New("AUTOMATION_RECEIPT_UNAVAILABLE")
	}
	return nil
}

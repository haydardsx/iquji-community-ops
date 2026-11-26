package automation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

func randomToken() string       { var b [32]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func tokenHash(t string) string { sum := sha256.Sum256([]byte(t)); return hex.EncodeToString(sum[:]) }

type Rule struct {
	Command string `json:"command"`
	Target  string `json:"target"`
}
type Client struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions"`
	Rules       []Rule       `json:"rules"`
}
type record struct {
	Client
	Hash string `json:"hash"`
}
type Grants struct {
	mu    sync.Mutex
	path  string
	items []record
}

func OpenGrants(dir string) (*Grants, error) {
	g := &Grants{path: filepath.Join(dir, "automation-clients.json")}
	data, err := os.ReadFile(g.path)
	if errors.Is(err, os.ErrNotExist) {
		return g, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	changed := false
	for _, entry := range entries {
		var item record
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(entry, &item); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(entry, &fields); err != nil {
			return nil, err
		}
		if _, exists := fields["permissions"]; !exists {
			item.Permissions = legacyPermissions()
			changed = true
		} else {
			// 仅旧磁盘记录缺少模式时迁移；新请求及显式空值均须通过正常校验。
			var saved []map[string]json.RawMessage
			if err := json.Unmarshal(fields["permissions"], &saved); err != nil {
				return nil, err
			}
			for i, permission := range saved {
				if _, hasMode := permission["mode"]; !hasMode {
					item.Permissions[i].Mode = ModeDirect
					changed = true
				}
			}
		}
		client, err := makeClient(item.ID, item.Name, item.Permissions, item.Rules)
		if err != nil {
			return nil, err
		}
		item.Client = client
		g.items = append(g.items, item)
	}
	if changed {
		err = g.save(g.items)
	}
	return g, err
}

func (g *Grants) save(items []record) error {
	b, err := json.Marshal(items)
	if err != nil {
		return err
	}
	if err = atomicSave(g.path, b); err == nil {
		g.items = items
	}
	return err
}

func atomicSave(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".automation-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (g *Grants) Add(name string, permissions []Permission, rules []Rule) (Client, string, error) {
	c, err := makeClient(randomToken()[:16], name, permissions, rules)
	if err != nil {
		return Client{}, "", err
	}
	token := randomToken()
	g.mu.Lock()
	defer g.mu.Unlock()
	items := append(append([]record{}, g.items...), record{c, tokenHash(token)})
	return c, token, g.save(items)
}
func (g *Grants) Update(id, name string, permissions []Permission, rules []Rule) (Client, error) {
	c, err := makeClient(id, name, permissions, rules)
	if err != nil {
		return Client{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	items := append([]record{}, g.items...)
	for i := range items {
		if items[i].ID == id {
			items[i].Client = c
			return c, g.save(items)
		}
	}
	return Client{}, errors.New("AUTOMATION_CLIENT_NOT_FOUND")
}
func (g *Grants) Remove(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	items := make([]record, 0, len(g.items))
	for _, r := range g.items {
		if r.ID != id {
			items = append(items, r)
		}
	}
	return g.save(items)
}
func (g *Grants) List() []Client {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []Client{}
	for _, r := range g.items {
		out = append(out, r.Client)
	}
	return out
}
func (g *Grants) Auth(token string) (Client, bool) {
	h := tokenHash(token)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range g.items {
		if r.Hash == h {
			return r.Client, true
		}
	}
	return Client{}, false
}

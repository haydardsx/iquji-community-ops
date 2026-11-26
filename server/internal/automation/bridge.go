package automation

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

type Ticket struct {
	ID       string `json:"id"`
	Client   string `json:"client"`
	ClientID string `json:"clientId"`
	Call     Call   `json:"call"`
	Window   bool   `json:"window"`
	Confirm  bool   `json:"confirm"`
	ctx      context.Context
	started  bool
	answered bool
	reply    chan Response
}
type Response struct {
	Value    json.RawMessage `json:"value,omitempty"`
	Error    string          `json:"error,omitempty"`
	Approved bool            `json:"approved,omitempty"`
	started  bool
}

type Bridge struct {
	mu       sync.Mutex
	queue    chan *Ticket
	pending  map[string]*Ticket
	lastPoll time.Time
}

func NewBridge() *Bridge {
	return &Bridge{queue: make(chan *Ticket, 16), pending: map[string]*Ticket{}}
}
func (b *Bridge) Ask(ctx context.Context, c Client, call Call, window, confirm bool) (Response, error) {
	t := &Ticket{ID: randomToken(), Client: c.Name, ClientID: c.ID, Call: call, Confirm: confirm, Window: window, ctx: ctx, reply: make(chan Response, 1)}
	b.mu.Lock()
	if time.Since(b.lastPoll) > 20*time.Second {
		b.mu.Unlock()
		return Response{}, errors.New("AUTOMATION_WINDOW_OFFLINE")
	}
	b.pending[t.ID] = t
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, t.ID); b.mu.Unlock() }()
	select {
	case b.queue <- t:
	default:
		return Response{}, errors.New("AUTOMATION_WINDOW_BUSY")
	}
	select {
	case v := <-t.reply:
		if v.Error != "" {
			return v, errors.New(v.Error)
		}
		return v, nil
	case <-ctx.Done():
		b.mu.Lock()
		started := t.started
		b.mu.Unlock()
		return Response{started: started}, errors.New("AUTOMATION_REQUEST_EXPIRED")
	}
}
func (b *Bridge) Next(ctx context.Context) *Ticket {
	for {
		b.mu.Lock()
		b.lastPoll = time.Now()
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil
		case t := <-b.queue:
			b.mu.Lock()
			live := b.pending[t.ID] != nil && t.ctx.Err() == nil
			b.mu.Unlock()
			if live {
				return t
			}
		}
	}
}
func (b *Bridge) Active(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.pending[id]
	return t != nil && !t.answered && t.ctx.Err() == nil
}

// Begin 只允许有效票据开始一次窗口操作，过期或撤销后无法再执行。
func (b *Bridge) Begin(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.pending[id]
	if t == nil || t.ctx.Err() != nil || t.started || t.answered {
		return false
	}
	t.started = true
	return true
}

func (b *Bridge) Reply(id string, v Response) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t := b.pending[id]; t != nil && !t.answered && t.ctx.Err() == nil && (t.started || v.Error != "") {
		v.started = t.started
		select {
		case t.reply <- v:
			t.answered = true
			return true
		default:
		}
	}
	return false
}

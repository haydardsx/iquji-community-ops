package automation

import (
	"context"
	"errors"
)

var errClientUnauthorized = errors.New("AUTOMATION_CLIENT_UNAUTHORIZED")
var errPermissionDenied = errors.New("AUTOMATION_PERMISSION_DENIED")

type pendingCall struct {
	clientID    string
	call        Call
	preapproved bool
	cancel      context.CancelCauseFunc
}

// 注册请求与修改授权共用一把锁，外部 I/O 在锁外进行，撤销可立即取消等待。
func (s *Service) register(ctx context.Context, token string, call Call) (context.Context, Client, func(), error) {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	c, ok := s.grants.Auth(token)
	if !ok {
		return nil, Client{}, nil, errClientUnauthorized
	}
	if !c.Permits(call) {
		if call.Name == GuideCommand && call.Args["target"] == "" && c.guideRequiresTarget(call.Args["action"]) {
			return nil, Client{}, nil, errArgumentRequired
		}
		return nil, Client{}, nil, errPermissionDenied
	}
	ctx, cancel := context.WithCancelCause(ctx)
	p := &pendingCall{c.ID, call, c.Preapproves(call), cancel}
	s.requests[p] = true
	done := func() {
		s.policyMu.Lock()
		delete(s.requests, p)
		s.policyMu.Unlock()
		cancel(nil)
	}
	return ctx, c, done, nil
}

// 已排队请求沿用原有确认要求；取消权限或撤销原预授权都会使旧请求失效。
func (s *Service) invalidate(id string, updated *Client) {
	for p := range s.requests {
		if p.clientID != id {
			continue
		}
		if updated == nil {
			p.cancel(errClientUnauthorized)
		} else if !updated.Permits(p.call) || p.preapproved && !updated.Preapproves(p.call) {
			p.cancel(errPermissionDenied)
		}
	}
}

func denied(ctx context.Context) error {
	cause := context.Cause(ctx)
	if errors.Is(cause, errPermissionDenied) || errors.Is(cause, errClientUnauthorized) {
		return cause
	}
	return nil
}

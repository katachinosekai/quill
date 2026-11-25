package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
)

type Account struct {
	Type     string
	Email    string
	PlanType string
}

type AccountSnapshot struct {
	Account            *Account
	RequiresOpenAIAuth bool
	Limits             map[string]AccountLimit
}

type AccountLimit struct {
	LimitID   string
	LimitName string
	PlanType  string
	Primary   *LimitWindow
	Secondary *LimitWindow
	Credits   *struct {
		HasCredits bool
		Unlimited  bool
		Balance    string
	}
}

type LimitWindow struct {
	UsedPercent        float64
	WindowDurationMins int
	ResetsAt           int64
}

type accountRPC struct {
	ctx             context.Context
	in              *json.Encoder
	output          <-chan readResult
	id              int
	loginCompletion *rpcMessage
}

func withAccountRPC(ctx context.Context, bin string, fn func(*accountRPC) error) error {
	return withAccountRPCAt(ctx, bin, "", fn)
}

func withAccountRPCAt(ctx context.Context, bin, cwd string, fn func(*accountRPC) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "app-server", "--listen", "stdio://")
	cmd.Env, cmd.Dir = serverEnv(), cwd
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer out.Close()
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()
	rpc := &accountRPC{ctx: ctx, in: json.NewEncoder(in), output: lines(ctx, out)}
	init := map[string]any{"clientInfo": map[string]string{"name": "quill", "title": "Quill", "version": "1"},
		"capabilities": map[string]bool{"experimentalApi": true}}
	if err := rpc.call("initialize", init, nil); err != nil {
		return err
	}
	if err := rpc.in.Encode(map[string]string{"method": "initialized"}); err != nil {
		return err
	}
	return fn(rpc)
}

func (r *accountRPC) next() (rpcMessage, error) {
	for {
		select {
		case <-r.ctx.Done():
			return rpcMessage{}, r.ctx.Err()
		case line, ok := <-r.output:
			if !ok {
				return rpcMessage{}, errors.New("Codex account connection closed")
			}
			if line.err != nil {
				return rpcMessage{}, line.err
			}
			var msg rpcMessage
			if json.Unmarshal(line.data, &msg) == nil {
				return msg, nil
			}
		}
	}
}

func (r *accountRPC) call(method string, params any, target any) error {
	r.id++
	if err := r.in.Encode(map[string]any{"id": r.id, "method": method, "params": params}); err != nil {
		return err
	}
	id := strconv.Itoa(r.id)
	for {
		msg, err := r.next()
		if err != nil {
			return err
		}
		if msg.Method == "account/login/completed" {
			r.loginCompletion = &msg
		}
		if msg.Method != "" || string(msg.ID) != id {
			continue
		}
		if msg.Error != nil {
			return fmt.Errorf("Codex could not complete %s", method)
		}
		if target == nil {
			return nil
		}
		return json.Unmarshal(msg.Result, target)
	}
}

func ReadAccount(ctx context.Context, bin string) (AccountSnapshot, error) {
	var snapshot AccountSnapshot
	err := withAccountRPC(ctx, bin, func(r *accountRPC) error {
		return r.call("account/read", map[string]bool{"refreshToken": false}, &snapshot)
	})
	return snapshot, err
}

// ReadAccountLimits is optional. Some credentials or subscription tiers
// report account identity but do not expose ChatGPT plan usage.
func ReadAccountLimits(ctx context.Context, bin string) (map[string]AccountLimit, error) {
	var limits struct {
		RateLimits          *AccountLimit
		RateLimitsByLimitID map[string]AccountLimit `json:"rateLimitsByLimitId"`
	}
	err := withAccountRPC(ctx, bin, func(r *accountRPC) error {
		return r.call("account/rateLimits/read", nil, &limits)
	})
	if err == nil && len(limits.RateLimitsByLimitID) == 0 && limits.RateLimits != nil {
		limits.RateLimitsByLimitID = map[string]AccountLimit{limits.RateLimits.LimitID: *limits.RateLimits}
	}
	return limits.RateLimitsByLimitID, err
}

func LoginChatGPT(ctx context.Context, bin string, open func(*url.URL)) error {
	return withAccountRPC(ctx, bin, func(r *accountRPC) error {
		var login struct{ LoginID, AuthURL string }
		if err := r.call("account/login/start", map[string]string{"type": "chatgpt"}, &login); err != nil {
			return err
		}
		u, err := url.Parse(login.AuthURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || login.LoginID == "" {
			return errors.New("Codex returned an invalid ChatGPT sign-in link")
		}
		open(u)
		for {
			var msg rpcMessage
			if r.loginCompletion != nil {
				msg = *r.loginCompletion
				r.loginCompletion = nil
			} else {
				msg, err = r.next()
				if err != nil {
					return err
				}
			}
			if msg.Method != "account/login/completed" {
				continue
			}
			var result struct {
				LoginID string
				Success bool
			}
			if json.Unmarshal(msg.Params, &result) != nil || result.LoginID != login.LoginID {
				continue
			}
			if !result.Success {
				return errors.New("ChatGPT sign-in did not complete")
			}
			return nil
		}
	})
}

func LogoutChatGPT(ctx context.Context, bin string) error {
	return withAccountRPC(ctx, bin, func(r *accountRPC) error {
		return r.call("account/logout", nil, nil)
	})
}

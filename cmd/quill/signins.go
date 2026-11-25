package main

import (
	"context"
	"net/url"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/account"
	"github.com/katachinosekai/quill/internal/codex"
)

type codexAuthState struct {
	snapshot   codex.AccountSnapshot
	ready      bool
	probing    bool
	busy       bool
	generation uint64
	cancel     context.CancelFunc
}

func (d *desk) refreshCodexAccount() {
	state := &d.codexAuth
	if state.probing || state.busy {
		return
	}
	bin, err := codexBinary()
	if err != nil {
		state.ready = false
		d.updateAccount()
		return
	}
	state.probing = true
	generation := state.generation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		snap, err := codex.ReadAccount(ctx, bin)
		if err == nil && snap.Account != nil && snap.Account.Type == "chatgpt" {
			snap.Limits, _ = codex.ReadAccountLimits(ctx, bin)
		}
		d.do(func() {
			state.probing = false
			if generation != state.generation {
				d.refreshCodexAccount()
				return
			}
			state.snapshot, state.ready = snap, err == nil
			d.updateAccount()
			if d.usage != nil {
				d.usage.update()
			}
		})
	}()
}

func (d *desk) updateAccount() {
	if d.acctBtn == nil {
		return
	}
	claude, chatgpt := "–", "–"
	if d.acct.LoggedIn {
		claude = "✓"
	}
	if d.signingIn != nil {
		claude = "…"
	}
	if acct := d.codexAuth.snapshot.Account; d.codexAuth.ready && acct != nil {
		if acct.Type == "chatgpt" {
			chatgpt = "✓"
		} else {
			chatgpt = "↔"
		}
	}
	if d.codexAuth.busy || d.codexAuth.probing && !d.codexAuth.ready {
		chatgpt = "…"
	}
	d.acctBtn.SetText("Sign-ins · Claude " + claude + " · ChatGPT " + chatgpt)
}

func (d *desk) accountMenu() {
	d.showMenu(d.claudeSignInMenu(), d.chatGPTSignInMenu())
}

func signInGroup(title, summary string, actions ...*fyne.MenuItem) *fyne.MenuItem {
	group := fyne.NewMenuItem(title, nil)
	status := fyne.NewMenuItem(summary, nil)
	status.Disabled = true
	group.ChildMenu = fyne.NewMenu(title, append([]*fyne.MenuItem{status}, actions...)...)
	return group
}

func (d *desk) claudeSignInMenu() *fyne.MenuItem {
	if d.signingIn != nil {
		return signInGroup("Claude", "Signing in…", fyne.NewMenuItem("Cancel sign-in", d.signingIn))
	}
	refresh := fyne.NewMenuItem("Refresh status", func() { go d.refreshAccount() })
	if !d.acct.LoggedIn {
		return signInGroup("Claude", "Signed out", fyne.NewMenuItem("Sign in to Claude", d.signIn), refresh)
	}
	signOut := fyne.NewMenuItem("Sign out", func() {
		dialog.ShowConfirm("Sign out of Claude?", "Claude Code terminal sessions on this computer share this sign-in.", func(ok bool) {
			if !ok {
				return
			}
			go func() {
				if err := account.Logout(d.cfg.ClaudeBin); err != nil {
					d.do(func() { dialog.ShowError(err, d.win) })
				}
				d.refreshAccount()
			}()
		}, d.win)
	})
	return signInGroup("Claude", accountSummary(d.acct.Email, d.acct.Plan()), refresh, signOut)
}

func accountSummary(email, plan string) string {
	summary := firstNonEmpty(email, "Signed in")
	if plan != "" {
		summary += " · " + plan
	}
	return summary
}

func planLabel(plan string) string {
	if plan == "" {
		return ""
	}
	return strings.ToUpper(plan[:1]) + strings.ReplaceAll(plan[1:], "_", " ")
}

func (d *desk) chatGPTSignInMenu() *fyne.MenuItem {
	state := &d.codexAuth
	if state.busy {
		if state.cancel != nil {
			return signInGroup("ChatGPT", "Signing in…", fyne.NewMenuItem("Cancel sign-in", state.cancel))
		}
		return signInGroup("ChatGPT", "Signing out…")
	}
	refresh := fyne.NewMenuItem("Refresh status", d.refreshCodexAccount)
	acct := state.snapshot.Account
	if !state.ready || acct == nil || acct.Type != "chatgpt" {
		status := "Signed out"
		switch {
		case !state.ready:
			status = "Status unavailable"
		case acct != nil || !state.snapshot.RequiresOpenAIAuth:
			status = "Codex uses another provider or sign-in method"
		}
		return signInGroup("ChatGPT", status, fyne.NewMenuItem("Sign in to ChatGPT", d.signInChatGPT), refresh)
	}
	signOut := fyne.NewMenuItem("Sign out", func() {
		dialog.ShowConfirm("Sign out of ChatGPT?", "Codex terminal sessions on this computer share this sign-in.", func(ok bool) {
			if ok {
				d.changeChatGPTSignIn(false)
			}
		}, d.win)
	})
	return signInGroup("ChatGPT", accountSummary(acct.Email, planLabel(acct.PlanType)), refresh, signOut)
}

func (d *desk) signInChatGPT() { d.changeChatGPTSignIn(true) }

func (d *desk) changeChatGPTSignIn(login bool) {
	state := &d.codexAuth
	if state.busy {
		return
	}
	bin, err := codexBinary()
	if err != nil {
		dialog.ShowError(err, d.win)
		return
	}
	limit := 15 * time.Second
	if login {
		limit = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	state.busy = true
	state.generation++
	if login {
		state.cancel = cancel
	}
	d.updateAccount()
	var prompt dialog.Dialog
	go func() {
		var err error
		if login {
			err = codex.LoginChatGPT(ctx, bin, func(link *url.URL) {
				d.do(func() {
					if ctx.Err() != nil {
						return
					}
					prompt = dialog.NewCustom("Sign in to ChatGPT", "Hide",
						widget.NewHyperlink("Continue at "+link.Hostname(), link), d.win)
					prompt.Show()
				})
			})
		} else {
			err = codex.LogoutChatGPT(ctx, bin)
		}
		userCanceled := ctx.Err() == context.Canceled
		cancel()
		d.do(func() {
			if prompt != nil {
				prompt.Hide()
			}
			state.busy, state.cancel = false, nil
			if err == nil {
				state.snapshot, state.ready = codex.AccountSnapshot{RequiresOpenAIAuth: true}, !login
				if d.usage != nil {
					d.usage.update()
				}
			} else if !userCanceled {
				dialog.ShowError(err, d.win)
			}
			d.updateAccount()
			d.refreshCodexAccount()
			d.trySwap()
		})
	}()
}

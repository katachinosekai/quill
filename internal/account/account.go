package account

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Status struct {
	LoggedIn     bool   `json:"loggedIn"`
	AuthMethod   string `json:"authMethod"`
	Email        string `json:"email"`
	OrgName      string `json:"orgName"`
	Subscription string `json:"subscriptionType"`
	Tier         string `json:"-"`
}

func (s Status) Plan() string {
	t := strings.ToLower(s.Tier)
	switch {
	case strings.Contains(t, "max_20x"):
		return "Max 20x"
	case strings.Contains(t, "max_5x"), strings.Contains(t, "max"):
		return "Max 5x"
	}
	if s.Subscription == "" {
		return ""
	}
	return strings.ToUpper(s.Subscription[:1]) + s.Subscription[1:]
}

func profileTier(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg struct {
		OAuth struct {
			OrgTier  string `json:"organizationRateLimitTier"`
			UserTier string `json:"userRateLimitTier"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	if cfg.OAuth.UserTier != "" {
		return cfg.OAuth.UserTier
	}
	return cfg.OAuth.OrgTier
}

func configPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude.json")
}

func Get(bin string) (Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "auth", "status", "--json").Output()
	var s Status
	if jerr := json.Unmarshal(out, &s); jerr != nil {
		if err != nil {
			return s, err
		}
		return s, jerr
	}
	if s.LoggedIn {
		s.Tier = profileTier(configPath())
	}
	return s, nil
}

var urlRe = regexp.MustCompile(`https://[^\s"'<>]+`)

func Login(ctx context.Context, bin string, onURL func(string)) error {
	cmd := exec.CommandContext(ctx, bin, "auth", "login")
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		seen := map[string]bool{}
		for sc.Scan() {
			for _, u := range urlRe.FindAllString(sc.Text(), -1) {
				if !seen[u] {
					seen[u] = true
					onURL(u)
				}
			}
		}
		close(done)
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	return err
}

func Logout(bin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, bin, "auth", "logout").Run()
}

package account

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"regexp"
	"time"
)

type Status struct {
	LoggedIn     bool   `json:"loggedIn"`
	AuthMethod   string `json:"authMethod"`
	Email        string `json:"email"`
	OrgName      string `json:"orgName"`
	Subscription string `json:"subscriptionType"`
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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/session"
)

func TestShowFilesInChat(t *testing.T) {
	bash := func(cmd string) *chat.Item {
		return &chat.Item{ToolName: "Bash", Input: []byte(`{"command":` + strconvQuote(cmd) + `}`)}
	}
	cases := map[string]string{
		"xdg-open /tmp/mock.png":                     "/tmp/mock.png",
		"xdg-open mock.png && echo done":             "/work/mock.png",
		"gio open https://example.com/page":          "https://example.com/page",
		"kde-open5 ~/a.pdf":                          "~/a.pdf",
		"/usr/bin/xdg-open /tmp/x.png 2>/dev/null &": "/tmp/x.png",
		"ls /tmp; gio info /tmp/x":                   "",
		"echo xdg-open /tmp/x.png":                   "",
	}
	for cmd, want := range cases {
		if got := strings.Join(openerTargets(bash(cmd), "/work"), " "); got != want {
			t.Errorf("%q: got %q want %q", cmd, got, want)
		}
	}
	if openerTargets(&chat.Item{ToolName: "Read", Input: []byte(`{"file_path":"/tmp/a"}`)}, "/") != nil {
		t.Error("only Bash")
	}
	send := &chat.Item{ToolName: "SendUserFile", Input: []byte(`{"file_path":"/tmp/shot.png"}`)}
	if sentFiles(send) != "/tmp/shot.png" {
		t.Errorf("SendUserFile path: %q", sentFiles(send))
	}
	real := &chat.Item{ToolName: "SendUserFile", Input: []byte(`{"files":["/tmp/ws-shots/1.png","/tmp/ws-shots/2.png"],"caption":"A / B","display":"render"}`)}
	if sentFiles(real) != "/tmp/ws-shots/1.png /tmp/ws-shots/2.png" {
		t.Errorf("SendUserFile files array: %q", sentFiles(real))
	}
	png := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(png, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(findMedia("see file://"+png)) != 1 || len(findMedia("see "+png+".")) != 1 {
		t.Error("file:// links and bare absolute paths both preview")
	}
	args := strings.Join(session.Options{Prompt: quillPromptHint}.Args(), "\n")
	if !strings.Contains(args, "--append-system-prompt\n"+quillPromptHint) {
		t.Error("every session gets the show-files hint")
	}
}

func strconvQuote(s string) string {
	b := []byte{'"'}
	for _, r := range s {
		if r == '"' || r == '\\' {
			b = append(b, '\\')
		}
		b = append(b, string(r)...)
	}
	return string(append(b, '"'))
}

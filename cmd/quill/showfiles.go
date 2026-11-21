package main

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/katachinosekai/quill/internal/chat"
)

const quillPromptHint = "You are running inside Quill, a desktop app for Claude Code sessions. Quill shows files in its chat: to show the user a file, image or link, write its absolute path or URL in your reply (or use SendUserFile). Do not open viewers or the browser with xdg-open, kde-open, gio open or similar."

const openerNotice = "Shown here instead of opening a window:"

const openerMessage = "Quill shows files and links in its chat instead of opening windows. Don't run xdg-open/kde-open/gio open; write the absolute path or URL in your reply and Quill previews it inline. (Quill already showed this one.)"

var openers = map[string]bool{"xdg-open": true, "kde-open": true, "kde-open5": true, "kde-open6": true, "gnome-open": true, "gio": true}

func openerTargets(it *chat.Item, cwd string) []string {
	if it.ToolName != "Bash" {
		return nil
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(it.Input, &in) != nil || in.Command == "" {
		return nil
	}
	segs, ok := shSegments(in.Command)
	if !ok {
		return nil
	}
	var out []string
	for _, seg := range segs {
		if len(seg) == 0 {
			continue
		}
		name := binName(seg[0].text)
		if !openers[name] {
			continue
		}
		args := seg[1:]
		if name == "gio" {
			if len(args) == 0 || args[0].text != "open" {
				continue
			}
			args = args[1:]
		}
		for _, a := range args {
			t := a.text
			if t == "" || strings.HasPrefix(t, "-") {
				continue
			}
			if !strings.Contains(t, "://") && !filepath.IsAbs(t) && !strings.HasPrefix(t, "~") {
				t = filepath.Join(cwd, t)
			}
			out = append(out, t)
		}
	}
	return out
}

func sentFiles(it *chat.Item) string {
	var in map[string]any
	if json.Unmarshal(it.Input, &in) != nil {
		return ""
	}
	var paths []string
	for _, k := range []string{"file_path", "path", "file", "files", "paths"} {
		switch v := in[k].(type) {
		case string:
			paths = append(paths, v)
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok {
					paths = append(paths, s)
				}
			}
		}
	}
	return strings.Join(paths, " ")
}

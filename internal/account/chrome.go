package account

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// chromeKey is Claude Code's own "Claude in Chrome enabled by default"
// setting (the toggle in its /config and /chrome panels), stored at the top
// level of the global config (~/.claude.json). Claude Code has no CLI command
// to set it, so Quill edits that one key in place.
const chromeKey = "claudeInChromeDefaultEnabled"

var chromeKeyRe = regexp.MustCompile(`(\n  "` + chromeKey + `":\s*)(true|false)`)

// ChromeDefault reports Claude Code's "Enabled by default" setting for
// Claude in Chrome (false when unset, like Claude Code).
func ChromeDefault() bool {
	return chromeDefaultAt(configPath())
}

// SetChromeDefault turns Claude Code's "Enabled by default" setting for
// Claude in Chrome on or off.
func SetChromeDefault(on bool) error {
	return setChromeDefaultAt(configPath(), on)
}

func chromeDefaultAt(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cfg map[string]json.RawMessage
	if json.Unmarshal(b, &cfg) != nil {
		return false
	}
	var v bool
	return json.Unmarshal(cfg[chromeKey], &v) == nil && v
}

// setChromeDefaultAt changes only that key's text, keeping the rest of the
// file byte for byte, checks the result still parses with the new value, and
// replaces the file atomically. Claude Code processes write this file too:
// if it changes while Quill edits, Quill starts over (a few tries).
func setChromeDefaultAt(path string, on bool) error {
	for try := 0; try < 5; try++ {
		old, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		next, err := withChromeDefault(old, on)
		if err != nil {
			return err
		}
		if bytes.Equal(old, next) {
			return nil
		}
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".claude.json.quill-*")
		if err != nil {
			return err
		}
		_, werr := tmp.Write(next)
		if cerr := tmp.Close(); werr == nil {
			werr = cerr
		}
		if werr == nil {
			werr = os.Chmod(tmp.Name(), st.Mode().Perm())
		}
		if werr != nil {
			os.Remove(tmp.Name())
			return werr
		}
		if now, err := os.ReadFile(path); err != nil || !bytes.Equal(now, old) {
			os.Remove(tmp.Name())
			continue
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			os.Remove(tmp.Name())
			return err
		}
		return nil
	}
	return errors.New("Claude Code kept changing ~/.claude.json; try again")
}

func withChromeDefault(old []byte, on bool) ([]byte, error) {
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(old, &cfg); err != nil {
		return nil, errors.New("~/.claude.json is not valid JSON: " + err.Error())
	}
	val := strconv.FormatBool(on)
	var next []byte
	switch locs := chromeKeyRe.FindAllSubmatchIndex(old, -1); {
	case len(locs) == 1:
		l := locs[0]
		next = append(append(append([]byte{}, old[:l[4]]...), val...), old[l[5]:]...)
	case len(locs) == 0 && cfg[chromeKey] == nil:
		i := bytes.IndexByte(old, '{')
		rest := bytes.TrimLeft(old[i+1:], " \t\r\n")
		sep := ",\n  "
		if len(rest) > 0 && rest[0] == '}' {
			sep = "\n"
		}
		ins := "\n  \"" + chromeKey + "\": " + val + sep
		next = append(append(append([]byte{}, old[:i+1]...), ins...), rest...)
	default:
		return nil, errors.New("unexpected layout of " + chromeKey + " in ~/.claude.json; change it in Claude Code's /config instead")
	}
	var check map[string]json.RawMessage
	if err := json.Unmarshal(next, &check); err != nil || len(check) != len(cfg)+boolInt(cfg[chromeKey] == nil) || string(check[chromeKey]) != val {
		return nil, errors.New("editing ~/.claude.json would not give valid JSON; nothing was changed")
	}
	return next, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

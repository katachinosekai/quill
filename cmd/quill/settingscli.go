package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/katachinosekai/quill/internal/session"
)

// `quill settings` (settingsreg.go): the Quill operator's way to read and
// change Quill Desktop's settings through quill.sock.

const settingsUsage = `usage:
  quill settings list [--json]
  quill settings get ID [--json]
  quill settings set ID VALUE --why "<the user's request>"
  quill settings undo [CHANGE]     (the newest change when CHANGE is left out)

list and get only read. set and undo work only from the Quill operator
session, in a turn the user started; security settings change only in
Settings.`

// settingsCLI runs `quill settings ARGS` against the Quill Desktop at sock.
func settingsCLI(sock string, args []string, getenv func(string) string, out, errw io.Writer) int {
	var words []string
	asJSON := false
	why := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			asJSON = true
		case a == "--why":
			if i+1 >= len(args) {
				fmt.Fprintln(errw, settingsUsage)
				return 2
			}
			i++
			why = args[i]
		case strings.HasPrefix(a, "--why="):
			why = strings.TrimPrefix(a, "--why=")
		case a == "-h" || a == "--help" || a == "help":
			fmt.Fprintln(out, settingsUsage)
			return 0
		default:
			words = append(words, a)
		}
	}
	if len(words) == 0 {
		fmt.Fprintln(errw, settingsUsage)
		return 2
	}
	r := settingsReq{Op: words[0]}
	n := map[string]int{"list": 1, "get": 2, "set": 3}[r.Op]
	switch r.Op {
	case "list", "get", "set":
		if len(words) != n {
			fmt.Fprintln(errw, settingsUsage)
			return 2
		}
		if n > 1 {
			r.ID = words[1]
		}
		if n > 2 {
			r.Value, r.Why = words[2], why
		}
	case "undo":
		if len(words) > 2 {
			fmt.Fprintln(errw, settingsUsage)
			return 2
		}
		if len(words) == 2 {
			v, err := strconv.Atoi(words[1])
			if err != nil || v < 1 {
				fmt.Fprintln(errw, "not a change number:", words[1])
				return 2
			}
			r.N = v
		}
	default:
		fmt.Fprintln(errw, settingsUsage)
		return 2
	}
	if r.Op == "set" || r.Op == "undo" {
		r.Session, r.Token = getenv(session.SessionIDEnv), getenv(session.TokenEnv)
	}
	b, _ := json.Marshal(r)
	lines, ok := commandReply(sock, "settings "+string(b))
	if !ok || len(lines) == 0 {
		fmt.Fprintln(errw, "no running Quill Desktop answered")
		return 1
	}
	var rep settingsReply
	if json.Unmarshal([]byte(lines[0]), &rep) != nil {
		fmt.Fprintln(errw, "Quill Desktop gave an answer this command doesn't understand (an older or newer build?)")
		return 1
	}
	if rep.Error != "" {
		fmt.Fprintln(errw, "quill settings:", rep.Error)
		return 1
	}
	if asJSON {
		var v any = rep.Settings
		if r.Op == "get" && len(rep.Settings) == 1 {
			v = rep.Settings[0]
		} else if rep.Change != nil {
			v = rep.Change
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
		return 0
	}
	switch r.Op {
	case "list":
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tVALUE\tSETTING")
		for _, s := range rep.Settings {
			if s.Security {
				fmt.Fprintf(tw, "%s\t-\t%s (security: only in %s)\n", s.ID, s.Label, s.Where)
				continue
			}
			fmt.Fprintf(tw, "%s\t%s\t%s (%s)%s\n", s.ID, shownValue(s.Value), s.Label, s.Section, valuesNote(s))
		}
		_ = tw.Flush()
	case "get":
		for _, s := range rep.Settings {
			fmt.Fprintf(out, "%s: %s (%s)\n", s.ID, s.Label, s.Section)
			if s.Security {
				fmt.Fprintln(out, "security setting: the user changes it in "+s.Where)
				continue
			}
			fmt.Fprintln(out, "value: "+shownValue(s.Value))
			if len(s.Values) > 0 {
				fmt.Fprintln(out, "values: "+strings.Join(s.Values, ", "))
			}
			if s.Hint != "" {
				fmt.Fprintln(out, "takes: "+s.Hint)
			}
			if s.Note != "" {
				fmt.Fprintln(out, "can't change here: "+s.Note)
			}
		}
	case "set":
		c := rep.Change
		if c == nil {
			return 0
		}
		if rep.Same {
			fmt.Fprintf(out, "%s is already %s; nothing changed.\n", c.Label, shownValue(c.New))
			return 0
		}
		fmt.Fprintf(out, "Changed %s: %s → %s (change %d). Quill Desktop showed the user a change card; undo with: quill settings undo %d\n",
			c.Label, shownValue(c.Old), shownValue(c.New), c.N, c.N)
	case "undo":
		if c := rep.Change; c != nil {
			fmt.Fprintf(out, "Undid change %d: %s is %s again.\n", c.N, c.Label, shownValue(c.Old))
		}
	}
	return 0
}

func valuesNote(s settingInfo) string {
	note := ""
	if s.Type == settingEnum && len(s.Values) <= 6 {
		note = " [" + strings.Join(s.Values, "|") + "]"
	}
	if s.Note != "" {
		note += " (can't change here: " + s.Note + ")"
	}
	return note
}

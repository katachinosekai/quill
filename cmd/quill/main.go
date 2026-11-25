package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/storage"

	"github.com/katachinosekai/quill/internal/live"
	"github.com/katachinosekai/quill/internal/transcript"
)

var version = "dev"

func listableURI(path string) (fyne.ListableURI, error) {
	return storage.ListerForURI(storage.NewFileURI(path))
}

const appID = "io.github.katachinosekai.quill"

func audioIdentity() {
	if os.Getenv("PULSE_PROP_OVERRIDE") == "" {
		os.Setenv("PULSE_PROP_OVERRIDE", "application.name='Quill' application.id='"+appID+"' application.icon_name='"+appID+"'")
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "codex-bridge" {
		os.Exit(runCodexBridge(os.Args[2:]))
	}
	// First: a live reload's device-lock state (pclock_handover_unix.go). Only
	// the window process uses it; any other path drops it (devices lock).
	handedUnlocks = takeUnlocks()
	audioIdentity()
	showVersion := flag.Bool("version", false, "print the version")
	claudeBin := flag.String("claude", "claude", "claude CLI to run")
	reload := flag.Bool("reload", false, "ask the running Quill to switch to this build now")
	heapFlag := flag.Bool("heap", false, "write a heap profile of the running Quill into $XDG_RUNTIME_DIR/quill-heap-<pid>.pprof")
	resumeAll := flag.Bool("resume-all", false, "ask the running Quill Desktop to reopen the kmem topics that were live when it last quit, as fresh sessions (before topic tabs: resume every ended kmem session in Inactive, or Archive with the argument archive, from its handoff)")
	resumeOne := flag.String("resume", "", "ask the running Quill to open the session with this kmem name, or start one from its handoff")
	kmemMessage := flag.String("kmem-message", "", "deliver this message (the Monday check-in) to Quill Desktop's running kmem assistant")
	kmemWindow := flag.Bool("kmem-window", false, "ask the running Quill Desktop to open its kmem window (the Monday check-in uses it)")
	hideTopic := flag.String("hide-topic", "", "ask the running Quill Desktop to hide these kmem topics (SLUG[,SLUG...]) under Dormant in its \"Open a topic\" card until they are opened")
	showTopic := flag.String("show-topic", "", "ask the running Quill Desktop to show these hidden kmem topics (SLUG[,SLUG...]) in its \"Open a topic\" card again")
	profile := flag.Bool("profile", false, "record 10 s of the running Quill's CPU use into $XDG_RUNTIME_DIR/quill-cpu-<pid>.pprof")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	sock := instanceSocket()
	if flag.Arg(0) == "settings" {
		os.Exit(settingsCLI(sock, flag.Args()[1:], os.Getenv, os.Stdout, os.Stderr))
	}
	if *heapFlag {
		if !sendCommand(sock, "heap") {
			fmt.Fprintln(os.Stderr, "no running Quill answered")
			os.Exit(1)
		}
		return
	}
	if *profile {
		if !sendCommand(sock, "profile") {
			fmt.Fprintln(os.Stderr, "no running Quill answered")
			os.Exit(1)
		}
		return
	}
	if *kmemMessage != "" {
		if !sendCommand(sock, "kmem-message "+strings.Join(strings.Fields(*kmemMessage), " ")) {
			fmt.Fprintln(os.Stderr, "no running Quill answered")
			os.Exit(1)
		}
		return
	}
	for verb, list := range map[string]string{"hide-topic": *hideTopic, "show-topic": *showTopic} {
		if list == "" {
			continue
		}
		if !sendCommand(sock, verb+" "+strings.Join(strings.Fields(strings.ReplaceAll(list, " ", "")), "")) {
			fmt.Fprintln(os.Stderr, "no running Quill Desktop answered (or a topic name is not letters, digits and dashes)")
			os.Exit(1)
		}
		return
	}
	if *kmemWindow {
		if !sendCommand(sock, "kmem-window") {
			fmt.Fprintln(os.Stderr, "no running Quill answered")
			os.Exit(1)
		}
		return
	}
	if *resumeOne != "" {
		if !validKmemName(*resumeOne) {
			fmt.Fprintln(os.Stderr, "not a kmem name (letters, digits and dashes):", *resumeOne)
			os.Exit(2)
		}
		if !sendCommand(sock, "resume "+*resumeOne) {
			fmt.Fprintln(os.Stderr, "no running Quill answered")
			os.Exit(1)
		}
		return
	}
	if *resumeAll {
		cmd := "resume-all"
		if flag.Arg(0) == placeArchive {
			cmd += " " + placeArchive
		}
		if !sendCommand(sock, cmd) {
			fmt.Fprintln(os.Stderr, "no running Quill answered")
			os.Exit(1)
		}
		return
	}
	if *reload {
		os.Exit(reloadCLI(sock, os.Stdout))
	}
	if os.Getenv(restoreEnv) == "" && signalRunning(sock) {
		return
	}
	if os.Getenv(keeperEnv) == "" && os.Getenv("QUILL_NO_KEEPER") == "" {
		handedUnlocks = nil
		os.Exit(runKeeper(os.Args[1:], takeRestore()))
	}
	os.Exit(runWindowWith(*claudeBin, sock))
}

func runWindow() int {
	return runWindowWith(claudeFlag(), instanceSocket())
}

func claudeFlag() string {
	if f := flag.Lookup("claude"); f != nil {
		return f.Value.String()
	}
	return "claude"
}

const memorySoftLimit = 512 << 20

func runWindowWith(claudeBin, sock string) int {
	debug.SetMemoryLimit(memorySoftLimit)
	// Session tokens for the settings command (settingsreg.go); a handover
	// keeps the key its running sessions' tokens were made with.
	initSessionKey(os.Getenv(restoreEnv) != "")
	app.SetMetadata(fyne.AppMetadata{ID: appID, Name: "Quill", Version: version, Icon: appIcon})
	a := app.NewWithID(appID)
	a.Settings().SetTheme(deskTheme{})
	a.SetIcon(appIcon)
	w := a.NewWindow("Quill")
	d := newDesk(a, w, config{
		ProjectsRoot: transcript.DefaultRoot(),
		CodexRoot:    codexRoot(),
		SessionsDir:  live.DefaultDir(),
		ClaudeBin:    claudeBin,
	})
	d.animate = true
	d.kwin = newKwinRules()
	// One-time move to topic tabs once kmem has the end-of-session check
	// (topicmigrate.go).
	go d.migrateTopicsWhenReady()
	d.watchFocus()
	d.startNotifier()
	d.setupAtStart()
	d.keeper = newKeeperClient()
	w.SetContent(d.build())
	startHidden := false
	prefs := a.Preferences()
	var first *winGeom
	st := takeRestore()
	if st != nil {
		d.restore = st
		startHidden = st.Hidden
		first = &winGeom{W: st.Width, H: st.Height, Max: st.Max}
		if st.Max { // the handed-over size is the screen: keep the normal size
			if saved := loadGeom(prefs, geomMain); saved.valid() {
				first.W, first.H = saved.W, saved.H
			}
		}
	}
	g := geomOr(first, loadGeom(prefs, geomMain), winGeom{W: 1280, H: 860})
	if first != nil && !first.valid() {
		g.Max, g.Full = loadGeom(prefs, geomMain).Max, loadGeom(prefs, geomMain).Full
	}
	setAppID(w, appID)
	applyGeom(w, g)
	if st != nil {
		pos := *st
		pos.Max = false // applyGeom did it
		placeWindow(w, &pos)
	}
	d.saveGeom = trackGeom(w, prefs, geomMain, g, func() bool { return false })
	w.SetCloseIntercept(d.confirmClose)
	if !d.setupBackground() {
		startHidden = false
	}
	d.setupShortcuts()
	d.refreshLocalModels()
	go func() {
		time.Sleep(2 * time.Second)
		d.do(d.checkCrashReports)
	}()
	d.listenInstance(sock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.watch(ctx)
	if startHidden {
		d.hidden = true
		a.Run()
	} else {
		w.ShowAndRun()
	}
	return 0
}

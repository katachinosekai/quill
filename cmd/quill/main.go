package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
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
	audioIdentity()
	showVersion := flag.Bool("version", false, "print the version")
	claudeBin := flag.String("claude", "claude", "claude CLI to run")
	reload := flag.Bool("reload", false, "ask the running Quill to switch to this build now")
	heapFlag := flag.Bool("heap", false, "write a heap profile of the running Quill into $XDG_RUNTIME_DIR/quill-heap-<pid>.pprof")
	resumeAll := flag.Bool("resume-all", false, "ask the running Quill Desktop to reopen the kmem topics that were live when it last quit, as fresh sessions (before topic tabs: resume every ended kmem session in Inactive, or Archive with the argument archive, from its handoff)")
	resumeOne := flag.String("resume", "", "ask the running Quill to open the session with this kmem name, or start one from its handoff")
	kmemWindow := flag.Bool("kmem-window", false, "ask the running Quill Desktop to open its kmem window (the Monday check-in uses it)")
	profile := flag.Bool("profile", false, "record 10 s of the running Quill's CPU use into $XDG_RUNTIME_DIR/quill-cpu-<pid>.pprof")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	sock := instanceSocket()
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
	app.SetMetadata(fyne.AppMetadata{ID: appID, Name: "Quill", Version: version, Icon: appIcon})
	a := app.NewWithID(appID)
	a.Settings().SetTheme(deskTheme{})
	a.SetIcon(appIcon)
	w := a.NewWindow("Quill")
	d := newDesk(a, w, config{
		ProjectsRoot: transcript.DefaultRoot(),
		SessionsDir:  live.DefaultDir(),
		ClaudeBin:    claudeBin,
	})
	d.animate = true
	go func() {
		home, _ := os.UserHomeDir()
		if msg, err := updateKmem(home, bundledKmemDir()); err != nil {
			fmt.Fprintln(os.Stderr, "kmem update:", err)
		} else if msg != "" {
			fmt.Fprintln(os.Stderr, msg)
			d.kmemVer.reset()
			d.refreshKmemVer() // the version line must not keep offering the update just done
		}
		// One-time move to topic tabs once kmem has the end-of-session
		// check (topicmigrate.go); after the update so a new kmem counts.
		d.migrateTopicsWhenReady()
	}()
	d.watchFocus()
	d.startNotifier()
	d.setupAtStart()
	d.keeper = newKeeperClient()
	w.SetContent(d.build())
	w.Resize(fyne.NewSize(1280, 860))
	startHidden := false
	if st := takeRestore(); st != nil {
		d.restore = st
		startHidden = st.Hidden
		if st.Width > 200 && st.Height > 200 {
			w.Resize(fyne.NewSize(st.Width, st.Height))
		}
		placeWindow(w, st)
	}
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

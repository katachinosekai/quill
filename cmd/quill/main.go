package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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

func main() {
	showVersion := flag.Bool("version", false, "print the version")
	claudeBin := flag.String("claude", "claude", "claude CLI to run")
	reload := flag.Bool("reload", false, "ask the running Quill to switch to this build now")
	profile := flag.Bool("profile", false, "record 10 s of the running Quill's CPU use into $XDG_RUNTIME_DIR/quill-cpu-<pid>.pprof")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	sock := instanceSocket()
	if *profile {
		if !sendCommand(sock, "profile") {
			fmt.Fprintln(os.Stderr, "no running Quill answered")
			os.Exit(1)
		}
		return
	}
	if *reload {
		if !sendCommand(sock, "reload") {
			fmt.Fprintln(os.Stderr, "no running Quill answered")
			os.Exit(1)
		}
		return
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

func runWindowWith(claudeBin, sock string) int {
	a := app.NewWithID("io.github.katachinosekai.quill")
	a.Settings().SetTheme(deskTheme{})
	a.SetIcon(appIcon)
	w := a.NewWindow("Quill")
	d := newDesk(a, w, config{
		ProjectsRoot: transcript.DefaultRoot(),
		SessionsDir:  live.DefaultDir(),
		ClaudeBin:    claudeBin,
	})
	d.animate = true
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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

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
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	sock := instanceSocket()
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
	a := app.NewWithID("io.github.katachinosekai.quill")
	a.Settings().SetTheme(deskTheme{})
	a.SetIcon(appIcon)
	w := a.NewWindow("Quill")
	d := newDesk(a, w, config{
		ProjectsRoot: transcript.DefaultRoot(),
		SessionsDir:  live.DefaultDir(),
		ClaudeBin:    *claudeBin,
	})
	d.animate = true
	w.SetContent(d.build())
	w.Resize(fyne.NewSize(1280, 860))
	startHidden := false
	if st := takeRestore(); st != nil {
		d.restore = st
		startHidden = st.Hidden
		if st.Width > 200 && st.Height > 200 {
			w.Resize(fyne.NewSize(st.Width, st.Height))
		}
	}
	w.SetCloseIntercept(d.confirmClose)
	if !d.setupBackground() {
		startHidden = false
	}
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
	os.Exit(0)
}

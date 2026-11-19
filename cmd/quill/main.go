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
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
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
	w.SetContent(d.build())
	w.Resize(fyne.NewSize(1280, 860))
	w.SetCloseIntercept(d.confirmClose)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.watch(ctx)
	w.ShowAndRun()
	os.Exit(0)
}

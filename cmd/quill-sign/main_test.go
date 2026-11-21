package main

import "testing"

func TestReleaseFile(t *testing.T) {
	for n, want := range map[string]bool{
		"quill-0.1.16-1.fc44.x86_64.rpm": true, "Quill-0.1.16-x86_64.AppImage": true, "quill-0.1.16.tar.gz": true,
		"quill-android-0.9.13.apk": true, "quill-windows-amd64.exe": true, "quill-0.1.16-windows-amd64.zip": true,
		"SHA256SUMS": false, "notes.md": false,
	} {
		if releaseFile(n) != want {
			t.Errorf("releaseFile(%q) != %v", n, want)
		}
	}
}

package main

import (
	"reflect"
	"testing"
)

// t3851ba: kmem 1.15 needs --by-user to restore a rejected proposal; tasks
// and facts restore as before.
func TestBinRestoreArgsProposal(t *testing.T) {
	out := "[t1abc23] proposal, 0.2 days in bin, by placeholder: rejected by the user\n" +
		"    (placeholder-topic) Placeholder proposal title\n" +
		"[f1234567] fact, 1.0 days in bin, by placeholder: wrong\n" +
		"    (-) placeholder text\n"
	items := parseBin(out)
	if len(items) != 2 {
		t.Fatalf("parsed %d items", len(items))
	}
	if !items[0].isProposal() || items[1].isProposal() {
		t.Fatalf("isProposal: %v %v", items[0].isProposal(), items[1].isProposal())
	}
	if got, want := binRestoreArgs(items[0], "the user clicked Restore"), []string{"trash", "restore", "t1abc23", "--by-user", "the user clicked Restore"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("proposal: %q", got)
	}
	if got, want := binRestoreArgs(items[1], "x"), []string{"trash", "restore", "f1234567"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fact: %q", got)
	}
	if items[0].binMeta() != "proposal, 0.2 days in bin, by placeholder" || items[0].Preview != "(placeholder-topic) Placeholder proposal title" {
		t.Fatalf("head/preview: %q / %q", items[0].binMeta(), items[0].Preview)
	}
}

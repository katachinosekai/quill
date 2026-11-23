package main

import "testing"

// Read-only commands the plan-first gate refused on 2026-11-23 (kmem
// --version, pdftotext to stdout, gh api reads, command -v, for loops), and
// the writes that must still need a plan.
func TestReadOnlyMore(t *testing.T) {
	for cmd, want := range map[string]bool{
		// kmem
		"kmem --version":                  true,
		"kmem --help":                     true,
		"kmem -h":                         true,
		"kmem --as quill-gate --version":  true,
		"kmem --version add x":            false,
		"kmem add quill/x text":           false,
		"kmem task add x":                 false,
		"kmem --as quill-gate task add x": false,
		// pdftotext / pdfinfo
		`pdftotext -layout "/home/user/Desktop/New plugins - Claude.pdf" - | grep -v "^\s*$" | head -300`: true,
		"pdftotext a.pdf -":                   true,
		"pdftotext -f 2 -l 3 -layout a.pdf -": true,
		"pdfinfo /home/user/a.pdf":            true,
		"pdftotext a.pdf out.txt":             false,
		"pdftotext a.pdf":                     false,
		"pdftotext -layout a.pdf - out.txt":   false,
		"pdftotext -cfg x a.pdf -":            false,
		"pdftotext a.pdf - > out.txt":         false,
		"pdftotext $F -":                      false,
		// gh
		`gh api repos/OWNER/REPO/contents/plugins --jq '.[].name' 2>&1 | tr '\n' ' ' | head -c 3000`: true,
		"gh api repos/O/R/pulls --paginate --jq .[].title":                                           true,
		"gh api -X GET repos/O/R/issues":                                                             true,
		"gh api --method=get repos/O/R/issues":                                                       true,
		"gh api -H 'Accept: application/vnd.github.raw' repos/O/R/readme":                            true,
		`gh api graphql -f query='query { viewer { login } }'`:                                       true,
		"gh api -X POST repos/O/R/issues":                                                            false,
		"gh api --method=DELETE repos/O/R":                                                           false,
		"gh api -XPOST repos/O/R/issues":                                                             false,
		"gh api repos/O/R/issues -f title=x":                                                         false,
		"gh api repos/O/R/issues -F title=x":                                                         false,
		"gh api repos/O/R/issues --raw-field title=x":                                                false,
		"gh api repos/O/R/issues --input body.json":                                                  false,
		"gh api -H 'X-HTTP-Method-Override: POST' repos/O/R":                                         false,
		`gh api graphql -f query='mutation { addStar(input:{}) { clientMutationId } }'`:              false,
		`gh api graphql -f query='Mutation { x }'`:                                                   false,
		"gh api graphql -F query=@q.graphql":                                                         false,
		"gh api graphql":                                                                             false,
		"gh api a b":                                                                                 false,
		"gh api repos/O/R --jq .x > out.json":                                                        false,
		"gh pr view 12":                                                                              true,
		"gh pr list --state open":                                                                    true,
		"gh pr diff 12":                                                                              true,
		"gh pr checks 12":                                                                            true,
		"gh issue view 3 --comments":                                                                 true,
		"gh issue list -R O/R":                                                                       true,
		"gh release view v1.0":                                                                       true,
		"gh release list":                                                                            true,
		"gh repo view O/R":                                                                           true,
		"gh run view 99 --log":                                                                       true,
		"gh run list --limit 5":                                                                      true,
		"gh pr view 12 --web":                                                                        false,
		"gh repo view -w":                                                                            false,
		"gh pr merge 12":                                                                             false,
		"gh pr create":                                                                               false,
		"gh issue close 3":                                                                           false,
		"gh release create v1":                                                                       false,
		"gh release download v1":                                                                     false,
		"gh run download 99":                                                                         false,
		"gh repo clone O/R":                                                                          false,
		// command -v, type, which
		"command -v go":       true,
		"command -V go gopls": true,
		"type go":             true,
		"which go":            true,
		"command go version":  false,
		"command rm x":        false,
		"command -p rm x":     false,
		"command -v":          false,
		// cd chains
		"cd /repo && sed -n 20,60p cmd/quill/x.go; echo ----; sed -n 1,80p cmd/quill/y.go; echo ----; kmem --version; grep -rn \"a\\|b\" --include=*.go cmd/quill | grep -v _test | head": true,
		"cd /repo; git log -3": true,
		"cd x && rm y":         false,
		"cd x; mkdir y":        false,
		// loops
		`for p in a b; do echo "=== $p"; gh api repos/O/R/contents/plugins/$p --jq '.[].name' | tr '\n' ' '; done`: true,
		`for c in go gopls java; do printf "%-20s " $c; command -v $c || echo "-"; done`:                           true,
		"for f in a.go b.go; do sed -n 1,5p $f; done":                                                              true,
		"for f in *.go; do wc -l $f; done":                                                                         true,
		"for f in a b\ndo\n  cat $f\ndone":                                                                         true,
		"ls | while read f; do wc -l \"$f\"; done":                                                                 true,
		"ls | while IFS= read -r f; do echo \"$f\"; done":                                                          true,
		"if test -f x; then cat x; else echo none; fi":                                                             true,
		"for f in a b; do rm $f; done":                                                                             false,
		"for f in a b; do echo $f > out; done":                                                                     false,
		"for f in a b; do echo $f; done > out":                                                                     false,
		"for f in a b; do echo $f | tee out; done":                                                                 false,
		"for f in a b; do echo $f; done | tee out":                                                                 false,
		"for f in a b; do sed -i s/x/y/ $f; done":                                                                  false,
		"for s in 1p w/tmp/x; do sed -n $s f; done":                                                                false,
		"for a in --oneline --output=x; do git log $a; done":                                                       false,
		"for m in GET POST; do gh api -X $m repos/O/R; done":                                                       false,
		"for c in ls rm; do $c x; done":                                                                            false,
		"for c in ls sudo; do $c x; done":                                                                          false,
		"for f in a b; do echo $f; done; rm x":                                                                     false,
		"for f in a b; do echo $(rm x); done":                                                                      false,
		"for f in a b; do echo $f":                                                                                 false,
		"while true; do mkdir x; done":                                                                             false,
		"if true; then rm x; fi":                                                                                   false,
		"git worktree add ../x":                                                                                    false,
		"mkdir x":                                                                                                  false,
		"echo $(rm x)":                                                                                             false,
		"echo x >> f":                                                                                              false,
		"sed -i s/a/b/ f":                                                                                          false,
		// tricky: quoting, ||, 2>&1, head -c
		`grep -n "a; rm x" f || echo "-"`: true,
		"ls 2>&1 | head -c 100":           true,
		`'for' x in a; do echo $x; done`:  false,
		`echo for x in a`:                 true,
	} {
		if got := readOnlyCommand(cmd, nil); got != want {
			t.Errorf("readOnlyCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// Attempts to change a variable behind the parser's back, so that a checked
// value differs from the one bash uses.
func TestReadOnlyVariableTracking(t *testing.T) {
	for cmd, want := range map[string]bool{
		"S=1p; sed -n $S f": true,
		"S=1p; for i in 1 2; do sed -n $S f; S=w/tmp/x; done":                       false, // set again after use inside the loop
		"S=1p; for i in 1 2; do sed -n $S f; for S in w/tmp/x; do true; done; done": false,
		"S=1p; true && S=w/tmp/x; sed -n $S f":                                      false, // maybe set: either value
		"S=1p; if true; then S=w/tmp/x; fi; sed -n $S f":                            false,
		"S=1p; echo w/tmp/x | while read S; do true; done; sed -n $S f":             false,
		"S=1p; echo w/tmp/x | read S; sed -n $S f":                                  false,
		"S=1p; read S; sed -n $S f":                                                 false,
		"S=1p; printf -v S w/tmp/x; sed -n $S f":                                    false,
		"S=1p; echo ${S:=w/tmp/x}; sed -n $S f":                                     false,
		"S=1p; S=w/tmp/x | true; sed -n $S f":                                       false,
		"for S in 1p 2p; do true; done; sed -n $S f":                                true,
		"S=w/tmp/x; true && for S in 1p; do true; done; sed -n $S f":                false,
		"S=1p; for i in 1 2; do S=2p; sed -n $S f; done":                            true, // either value, both checked
		"S=1p; for i in 1 2; do S=w/tmp/x; sed -n $S f; done":                       false,
		"read -r $V":                                                  false,
		"git log {--output=x,-1}":                                     false,
		"git log '{--output=x,-1}'":                                   true,
		"for a in {--output=x,-1}; do git log $a; done":               false,
		"for x in a b; do for y in c d; do echo $x$y; done; done":     true,
		"for x in a b; do for y in c d; do sed -n $x$y f; done; done": false,
		"for x in 1 2; do for y in p p; do sed -n $x$y f; done; done": true,
	} {
		if got := readOnlyCommand(cmd, nil); got != want {
			t.Errorf("readOnlyCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

package codex

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestKmemHookReadiness(t *testing.T) {
	raw := json.RawMessage(`{
	  "data": [
	    {"cwd":"/probe","hooks":[
	      {"eventName":"preToolUse","matcher":"^Bash$","command":"/bin/kmem-guard","enabled":true,"trustStatus":"trusted"},
	      {"eventName":"sessionStart","command":"/bin/pzlock ensure","enabled":true,"trustStatus":"untrusted"},
	      {"eventName":"sessionStart","command":"/bin/kmem brief","enabled":false,"trustStatus":"trusted"},
	      {"eventName":"sessionStart","command":"echo kmem brief","enabled":true,"trustStatus":"trusted"}
	    ]},
	    {"cwd":"/other","hooks":[{"eventName":"sessionStart","command":"/bin/pzlock ensure","enabled":true,"trustStatus":"trusted"}]}
	  ]
	}`)
	got, err := parseKmemHooks(raw, "/probe")
	if want := (KmemHookStatus{Guard: "ready", Lock: "review", Brief: "disabled"}); err != nil || got != want {
		t.Fatalf("hook status %+v, %v, want %+v", got, err, want)
	}
	got, err = parseKmemHooks(raw, "/other")
	if err != nil || got.Guard != "missing" || got.Lock != "ready" || got.Brief != "missing" {
		t.Fatalf("workspace scoped status %+v, %v", got, err)
	}
	if kind := nativeKmemHook(hookDef{EventName: "preToolUse", Matcher: "^apply_patch$", Command: "kmem-guard"}); kind != "" {
		t.Fatalf("a non-shell matcher would not evaluate the probe: %q", kind)
	}
}

func TestKmemTopicEnvironment(t *testing.T) {
	env := []string{"X=1", "KMEM_SESSION=other"}
	if got := kmemTopicEnvironment(env, "zomboid"); !reflect.DeepEqual(got, []string{"X=1", "KMEM_SESSION=zomboid"}) {
		t.Fatalf("scoped env %v", got)
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const kmemAPI = 1

type kmemVersion struct {
	mu  sync.Mutex
	api int
	ver string // the installed kmem's version ("" if unknown)
	// features: `kmem version --json` "features" (1.10.0+: "topic-context",
	// "end-check", "sessions"); nil before.
	features []string
	at       time.Time
}

func (v *kmemVersion) reset() {
	v.mu.Lock()
	v.api, v.ver, v.features, v.at = 0, "", nil, time.Time{}
	v.mu.Unlock()
}

// kmemInstalledVersion: the version `kmem version --json` reports (cached
// with the API level; "" if unknown). Not on the UI thread.
func (d *desk) kmemInstalledVersion() string {
	d.kmemAPILevel()
	d.kmemVer.mu.Lock()
	defer d.kmemVer.mu.Unlock()
	return d.kmemVer.ver
}

func (d *desk) kmemAPILevel() int {
	v := &d.kmemVer
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.at.IsZero() && time.Since(v.at) < policyRefresh {
		return v.api
	}
	v.api, v.ver, v.features, v.at = 0, "", nil, time.Now()
	out, err := d.runKmem("kmem", "version", "--json")
	var r struct {
		API      int      `json:"api"`
		Version  string   `json:"version"`
		Features []string `json:"features"`
	}
	if err == nil && json.Unmarshal([]byte(strings.TrimSpace(out)), &r) == nil {
		v.api, v.ver, v.features = r.API, r.Version, r.Features
	}
	return v.api
}

// kmemHasFeature: the installed kmem lists feature f in `kmem version
// --json`. Not on the UI thread.
func (d *desk) kmemHasFeature(f string) bool {
	if d.kmemAPILevel() != kmemAPI {
		return false
	}
	d.kmemVer.mu.Lock()
	defer d.kmemVer.mu.Unlock()
	return contains(d.kmemVer.features, f)
}

// kmemFeatureCached: kmemHasFeature from the cache only, for the UI thread;
// false until the version was read once. A stale cache is refreshed in the
// background and the old answer is used meanwhile.
func (d *desk) kmemFeatureCached(f string) bool {
	v := &d.kmemVer
	v.mu.Lock()
	known, stale := !v.at.IsZero(), time.Since(v.at) >= policyRefresh
	ok := known && v.api == kmemAPI && contains(v.features, f)
	v.mu.Unlock()
	if stale && d.kmemAvailable() {
		go d.kmemAPILevel()
	}
	return ok
}

func (d *desk) kmemJSON(out any, args ...string) bool {
	return d.kmemJSONWith(d.runKmem, out, args...)
}

// kmemJSONShared is kmemJSON through the shared loader (runKmemShared).
func (d *desk) kmemJSONShared(out any, args ...string) bool {
	return d.kmemJSONWith(func(_ string, a ...string) (string, error) { return d.runKmemShared(a...) }, out, args...)
}

func (d *desk) kmemJSONWith(run func(string, ...string) (string, error), out any, args ...string) bool {
	if d.kmemAPILevel() != kmemAPI {
		return false
	}
	raw, err := run("kmem", append(args, "--json")...)
	if err != nil {
		return false
	}
	var api struct {
		API int `json:"api"`
	}
	raw = strings.TrimSpace(raw)
	return json.Unmarshal([]byte(raw), &api) == nil && api.API == kmemAPI && json.Unmarshal([]byte(raw), out) == nil
}

func (d *desk) kmemHandoffList() ([]kmemHandoff, error) {
	return d.kmemHandoffListWith(d.runKmem, d.kmemJSON)
}

// kmemHandoffListShared is for the window's loaders (runKmemShared).
func (d *desk) kmemHandoffListShared() ([]kmemHandoff, error) {
	return d.kmemHandoffListWith(func(_ string, a ...string) (string, error) { return d.runKmemShared(a...) }, d.kmemJSONShared)
}

func (d *desk) kmemHandoffListWith(run func(string, ...string) (string, error), js func(any, ...string) bool) ([]kmemHandoff, error) {
	var r struct {
		Handoffs []struct {
			Name  string `json:"name"`
			Saved string `json:"saved"`
		} `json:"handoffs"`
	}
	if js(&r, "handoff", "list") {
		var hs []kmemHandoff
		for _, h := range r.Handoffs {
			if t, err := time.Parse(time.RFC3339, h.Saved); err == nil {
				hs = append(hs, kmemHandoff{Name: h.Name, Saved: t})
			}
		}
		return hs, nil
	}
	out, err := run("kmem", "handoff", "list")
	if err != nil {
		return nil, err
	}
	return parseHandoffList(out), nil
}

func (d *desk) kmemHandoffText(name string) string {
	var r struct {
		Text string `json:"text"`
	}
	if d.kmemJSON(&r, "handoff", "get", name) {
		return r.Text
	}
	out, _ := d.runKmem("kmem", "handoff", "get", name)
	return out
}

func (d *desk) kmemPolicy() (handoffPolicy, bool) {
	var r struct {
		Policy struct {
			Soft int     `json:"soft"`
			Hard int     `json:"hard"`
			Mode string  `json:"mode"`
			TTL  *string `json:"ttl"`
		} `json:"policy"`
	}
	if d.kmemJSON(&r, "handoff", "policy") {
		line := fmt.Sprintf("soft=%d hard=%d mode=%s", r.Policy.Soft, r.Policy.Hard, r.Policy.Mode)
		if r.Policy.TTL != nil {
			line += " ttl=" + *r.Policy.TTL
		}
		return parsePolicy(line)
	}
	out, err := d.runKmem("kmem", "handoff", "policy")
	if err != nil {
		return handoffPolicy{}, false
	}
	return parsePolicy(out)
}

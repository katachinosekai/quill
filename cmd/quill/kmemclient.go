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
	at  time.Time
}

func (v *kmemVersion) reset() {
	v.mu.Lock()
	v.api, v.ver, v.at = 0, "", time.Time{}
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
	v.api, v.ver, v.at = 0, "", time.Now()
	out, err := d.runKmem("kmem", "version", "--json")
	var r struct {
		API     int    `json:"api"`
		Version string `json:"version"`
	}
	if err == nil && json.Unmarshal([]byte(strings.TrimSpace(out)), &r) == nil {
		v.api, v.ver = r.API, r.Version
	}
	return v.api
}

func (d *desk) kmemJSON(out any, args ...string) bool {
	if d.kmemAPILevel() != kmemAPI {
		return false
	}
	raw, err := d.runKmem("kmem", append(args, "--json")...)
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
	var r struct {
		Handoffs []struct {
			Name  string `json:"name"`
			Saved string `json:"saved"`
		} `json:"handoffs"`
	}
	if d.kmemJSON(&r, "handoff", "list") {
		var hs []kmemHandoff
		for _, h := range r.Handoffs {
			if t, err := time.Parse(time.RFC3339, h.Saved); err == nil {
				hs = append(hs, kmemHandoff{Name: h.Name, Saved: t})
			}
		}
		return hs, nil
	}
	out, err := d.runKmem("kmem", "handoff", "list")
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

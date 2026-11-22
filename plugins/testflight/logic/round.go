package main

// A round's test build (contributes.test_builds): when an Autopilot round
// ends, Enplace asks for one build of the product with what to try in it.
// TestFlight asks the builder as Send to TestFlight does, writes the
// round's list as the build's What to test, and follows the build until
// it's on the person's phone ("public.test_build").

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/testflight/beta"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

const keyRound = "round_build"

// roundBuild is the round build asked for last: Enplace's ask, and the
// build number it became.
type roundBuild struct {
	Ask     plugin.TestBuildAsk `json:"ask"`
	For     string              `json:"for,omitempty"`
	Version string              `json:"version,omitempty"`
	At      time.Time           `json:"at"`
	ReadyAt time.Time           `json:"ready_at,omitzero"`
	// Why is why it couldn't be asked for.
	Why string `json:"why,omitempty"`
}

// testBuild asks for a round's build.
func (r *run) testBuild(raw json.RawMessage) {
	var ask plugin.TestBuildAsk
	if json.Unmarshal(raw, &ask) != nil || ask.ID == "" {
		r.res.Toast = "That ask for a test build had no round in it."
		return
	}
	rb := roundBuild{Ask: ask, At: r.req.Now}
	if q, ok := r.asked(); ok {
		if where, _ := r.building(r.status()); where != "" {
			// One is on its way already: this round's changes are in it.
			rb.For = q.For
		}
	}
	if rb.For == "" {
		var why string
		rb.Version, rb.For, why = r.ask("A test build for " + ask.Term + " " + itoa(ask.Round))
		rb.Why = why
	}
	r.res.SetProject(r.pid, keyRound, rb)
	tb := r.testState(&rb, r.status())
	r.res.SetProject(r.pid, plugin.KeyTestBuild, tb)
	if rb.Why != "" {
		r.res.Toast = rb.Why
		return
	}
	r.res.Toast = "Asked " + r.builder().Name + " for a build of " + ask.Term + " " + itoa(ask.Round) + ". It goes to TestFlight once Apple has it."
	r.res.Log = "Asked for a test build of " + ask.Term + " " + itoa(ask.Round)
}

// trackTestBuild keeps the round build's state current, after a check.
func (r *run) trackTestBuild(s *Status) {
	var rb *roundBuild
	if !r.req.State.GetProject(r.pid, keyRound, &rb) || rb == nil {
		return
	}
	tb := r.testState(rb, s)
	if tb.State == plugin.TestReady && rb.ReadyAt.IsZero() {
		rb.ReadyAt = r.req.Now
		tb.ReadyAt = r.req.Now
		r.res.SetProject(r.pid, keyRound, rb)
	}
	r.res.SetProject(r.pid, plugin.KeyTestBuild, tb)
}

// testState says where a round build is, from TestFlight's builds and the
// builder's answer.
func (r *run) testState(rb *roundBuild, s *Status) plugin.TestBuild {
	tb := plugin.TestBuild{ID: rb.Ask.ID, Round: rb.Ask.Round, At: rb.At, ReadyAt: rb.ReadyAt, State: plugin.TestBuilding}
	if rb.Why != "" {
		tb.State, tb.Note = plugin.TestFailed, rb.Why
		return tb
	}
	if s != nil && s.AppID != "" {
		tb.Link = "https://beta.itunes.apple.com/v1/app/" + s.AppID
	}
	if s != nil {
		for _, b := range s.Builds {
			if b.Number != rb.For || b.Uploaded.Before(rb.At.Add(-time.Hour)) {
				continue
			}
			tb.Name = b.Version + " (" + b.Number + ")"
			switch b.State {
			case beta.Ready:
				tb.State = plugin.TestReady
			case beta.NeedsAnswer:
				tb.State, tb.Note = plugin.TestWaiting, "Waits for the encryption answer: answer it on the TestFlight card."
			case beta.Processing:
				tb.Note = "Apple is processing it."
			default:
				tb.State, tb.Note = plugin.TestFailed, beta.StateName(b.State)+"."
			}
			return tb
		}
	}
	if rb.Version != "" {
		tb.Name = rb.Version + " (" + rb.For + ")"
	}
	where, failed := r.building(s)
	switch {
	case failed != "":
		tb.State, tb.Note = plugin.TestFailed, failed
	case where != "":
		tb.Note = where + "."
	case r.req.Now.Sub(rb.At) > 3*time.Hour:
		tb.State, tb.Note = plugin.TestFailed, "It never reached App Store Connect. See the builder's runs."
	}
	return tb
}

// roundText is the round's What to test for a build, when the build is a
// round's.
func (r *run) roundText(number string) string {
	var rb *roundBuild
	if !r.req.State.GetProject(r.pid, keyRound, &rb) || rb == nil || rb.For != number {
		return ""
	}
	return rb.Ask.WhatToTest
}

func itoa(n int) string { return strconv.Itoa(n) }

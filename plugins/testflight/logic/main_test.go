//go:build !wasip1

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/testflight/beta"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

const pid = plugintest.ProjectID

// asc stands in for App Store Connect: each answer by "METHOD path", and
// every call it was sent.
type asc struct {
	answers map[string]string
	sent    []string
	bodies  map[string]string
}

func fake(t *testing.T, answers map[string]string) *asc {
	f := &asc{answers: answers, bodies: map[string]string{}}
	plugin.HostFunc = func(raw []byte) []byte {
		var in struct {
			Fetch plugin.FetchRequest `json:"fetch"`
		}
		_ = json.Unmarshal(raw, &in)
		m := in.Fetch.Method
		if m == "" {
			m = "GET"
		}
		path := strings.TrimPrefix(in.Fetch.URL, "https://api.appstoreconnect.apple.com")
		path, _, _ = strings.Cut(path, "?")
		k := m + " " + path
		f.sent = append(f.sent, k)
		f.bodies[k] = string(in.Fetch.Body)
		status, body := 404, `{"errors":[{"detail":"not here"}]}`
		if a, ok := f.answers[k]; ok {
			status, body = 200, a
		}
		out, _ := json.Marshal(map[string]any{"fetch": plugin.FetchResponse{Status: status, Body: []byte(body)}})
		return out
	}
	t.Cleanup(func() { plugin.HostFunc = nil; fetched = 0 })
	return f
}

func key(t *testing.T) string {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func request(t *testing.T, hook string, settings map[string]any) *plugin.Request {
	req := plugintest.Request(hook)
	req.Plugin.ID = "testflight"
	req.ProjectSettings = map[string]map[string]any{pid: settings}
	req.Secrets = map[string]string{"asc_issuer_id": "iss", "asc_key_id": "KEY1", "asc_private_key": key(t)}
	req.Peers = map[string]plugin.State{}
	publish(req, "app", "public.app", map[string]string{"bundle_id": "com.guedio.planit", "name": "Plan It"})
	return req
}

func publish(req *plugin.Request, id, k string, v any) {
	st := req.Peers[id]
	st.PutProject(pid, k, v)
	req.Peers[id] = st
}

func stored(t *testing.T, res *plugin.Response) *Status {
	t.Helper()
	var s *Status
	if res.State == nil || !res.State.GetProject(pid, keyStatus, &s) {
		t.Fatal("no status stored")
	}
	return s
}

func keep(req *plugin.Request, res *plugin.Response) {
	if res.State == nil {
		return
	}
	for k, v := range res.State.Projects[pid] {
		req.State.PutProject(pid, k, json.RawMessage(v))
	}
}

func build(id, number, processing, internal string, uploaded time.Time, notes string) string {
	loc := `[]`
	inc := ""
	if notes != "" {
		loc = `[{"type":"betaBuildLocalizations","id":"L` + id + `"}]`
		inc = `,{"type":"betaBuildLocalizations","id":"L` + id + `","attributes":{"locale":"en-US","whatsNew":"` + notes + `"}}`
	}
	return `{"data":[{"id":"` + id + `","attributes":{"version":"` + number + `","uploadedDate":"` + uploaded.Format(time.RFC3339) + `","processingState":"` + processing + `"},
	"relationships":{"preReleaseVersion":{"data":{"type":"preReleaseVersions","id":"p1"}},"buildBetaDetail":{"data":{"type":"buildBetaDetails","id":"d` + id + `"}},"betaBuildLocalizations":{"data":` + loc + `}}}],
	"included":[{"type":"preReleaseVersions","id":"p1","attributes":{"version":"1.0"}},{"type":"buildBetaDetails","id":"d` + id + `","attributes":{"internalBuildState":"` + internal + `"}}` + inc + `]}`
}

func TestStepsBeforeAnythingIsSetUp(t *testing.T) {
	req := plugintest.Request(plugin.HookRender)
	req.Slots = []string{plugin.SlotSettingsPanel, "project_card:testflight"}
	res := plugintest.Call(t, handle, req)
	list := res.Cards[plugin.SlotSettingsPanel][1]
	if len(list.Items) != 8 || list.Items[0].Done || !strings.Contains(list.Items[6].Title, "Xcode Cloud") {
		t.Fatalf("steps %+v", list.Items)
	}
	if c := res.Cards["project_card:testflight"]; c[0].Value != "Not set up yet" || !strings.Contains(c[1].Text, "bundle ID") {
		t.Fatalf("card %+v", c)
	}
}

func TestRefreshSetsUpTheGroupAndWritesWhatToTest(t *testing.T) {
	now := plugintest.Now
	f := fake(t, map[string]string{
		"GET /v1/apps":                      `{"data":[{"type":"apps","id":"900"}]}`,
		"GET /v1/apps/900/betaGroups":       `{"data":[]}`,
		"POST /v1/betaGroups":               `{"data":{"type":"betaGroups","id":"g1"}}`,
		"GET /v1/betaGroups/g1/betaTesters": `{"data":[]}`,
		"POST /v1/betaTesters":              `{"data":{"type":"betaTesters","id":"t1"}}`,
		"GET /v1/builds":                    build("b12", "12", "VALID", "READY_FOR_BETA_TESTING", now.Add(-time.Hour), ""),
		"POST /v1/betaBuildLocalizations":   `{"data":{"type":"betaBuildLocalizations","id":"L1"}}`,
	})
	req := request(t, plugin.HookSchedule, map[string]any{"testers": "justin@example.com"})
	req.Work = []plugin.Work{
		{ID: "w1", Key: "PLAN-9", ProjectID: pid, Title: "Make a trip", StageKind: "done", DoneAt: now.Add(-2 * time.Hour)},
		{ID: "w2", Key: "PLAN-10", ProjectID: pid, Title: "Not done", StageKind: "doing"},
		{ID: "w3", Key: "X-1", ProjectID: "other", Title: "Another project", StageKind: "done", DoneAt: now.Add(-2 * time.Hour)},
	}
	res := plugintest.Call(t, handle, req)
	s := stored(t, res)
	if s.AppID != "900" || s.Group == nil || s.Group.ID != "g1" || len(s.Builds) != 1 || s.Builds[0].State != beta.Ready || s.Error != "" {
		t.Fatalf("status %+v (sent %v)", s, f.sent)
	}
	if g := f.bodies["POST /v1/betaGroups"]; !strings.Contains(g, `"isInternalGroup":true`) || !strings.Contains(g, `"hasAccessToAllBuilds":true`) || !strings.Contains(g, `"name":"Me"`) {
		t.Fatalf("group %s", g)
	}
	if b := f.bodies["POST /v1/betaTesters"]; !strings.Contains(b, "justin@example.com") {
		t.Fatalf("tester %s", b)
	}
	notes := f.bodies["POST /v1/betaBuildLocalizations"]
	if !strings.Contains(notes, "Make a trip") || strings.Contains(notes, "Not done") || strings.Contains(notes, "Another project") {
		t.Fatalf("what to test %s", notes)
	}
	var pub []map[string]any
	res.State.GetProject(pid, "public.builds", &pub)
	if len(pub) != 1 || pub[0]["version"] != "1.0" || pub[0]["build"] != "12" || pub[0]["state"] != "ready" {
		t.Fatalf("public.builds %v", pub)
	}

	// Next time the group is there and the notes are written: nothing changes.
	f.answers["GET /v1/apps/900/betaGroups"] = `{"data":[{"id":"g1","attributes":{"name":"Me","isInternalGroup":true,"hasAccessToAllBuilds":true}}]}`
	f.answers["GET /v1/betaGroups/g1/betaTesters"] = `{"data":[{"attributes":{"email":"justin@example.com","state":"INSTALLED"}}]}`
	f.answers["GET /v1/builds"] = build("b12", "12", "VALID", "READY_FOR_BETA_TESTING", now.Add(-time.Hour), "New")
	f.sent = nil
	plugintest.Call(t, handle, req)
	for _, k := range f.sent {
		if strings.HasPrefix(k, "POST") || strings.HasPrefix(k, "PATCH") {
			t.Fatalf("changed something on a second check: %v", f.sent)
		}
	}
}

func TestABuildWaitingOnTheEncryptionAnswer(t *testing.T) {
	f := fake(t, map[string]string{
		"GET /v1/apps":                      `{"data":[{"type":"apps","id":"900"}]}`,
		"GET /v1/apps/900/betaGroups":       `{"data":[{"id":"g1","attributes":{"name":"Me","isInternalGroup":true}}]}`,
		"GET /v1/betaGroups/g1/betaTesters": `{"data":[]}`,
		"GET /v1/builds":                    build("b3", "3", "VALID", "MISSING_EXPORT_COMPLIANCE", plugintest.Now.Add(-time.Hour), "x"),
		"PATCH /v1/builds/b3":               `{}`,
	})
	req := request(t, plugin.HookSchedule, nil)
	res := plugintest.Call(t, handle, req)
	keep(req, res)
	req.Hook, req.Slots = plugin.HookRender, []string{"project_card:testflight"}
	card := plugintest.Call(t, handle, req).Cards["project_card:testflight"]
	var btn *plugin.Button
	for _, b := range card {
		for i := range b.Buttons {
			if b.Buttons[i].Action == "no_encryption" {
				btn = &b.Buttons[i]
			}
		}
	}
	if card[0].Sub == "" || btn == nil || btn.Confirm == "" {
		t.Fatalf("card %+v", card)
	}
	req.Hook, req.Action = plugin.HookAction, &plugin.Action{Name: "no_encryption", Args: btn.Args}
	plugintest.Call(t, handle, req)
	if b := f.bodies["PATCH /v1/builds/b3"]; !strings.Contains(b, `"usesNonExemptEncryption":false`) {
		t.Fatalf("answer %s", b)
	}
}

func readyStatus(req *plugin.Request, builds ...beta.Build) {
	req.State.PutProject(pid, keyStatus, &Status{BundleID: "com.guedio.planit", AppName: "Plan It", Keys: true, AppID: "900", Builds: builds})
}

func TestSendAsksXcodeCloud(t *testing.T) {
	req := request(t, plugin.HookAction, nil)
	readyStatus(req)
	req.Action = &plugin.Action{Name: "send"}
	if res := plugintest.Call(t, handle, req); !strings.Contains(res.Toast, "Xcode Cloud plugin") {
		t.Fatalf("without Xcode Cloud: %q", res.Toast)
	}
	publish(req, "xcode-cloud", "public.ci", map[string]any{"ready": true})
	res := plugintest.Call(t, handle, req)
	var q map[string]any
	if !res.State.GetProject(pid, "public.ci_request", &q) || !strings.HasPrefix(q["id"].(string), "testflight-") {
		t.Fatalf("request %v (%q)", q, res.Toast)
	}
	keep(req, res)

	// While it's asked, or running, it isn't asked again.
	if res := plugintest.Call(t, handle, req); !strings.Contains(res.Toast, "already") {
		t.Fatalf("asked twice: %q", res.Toast)
	}
	publish(req, "xcode-cloud", "public.ci", map[string]any{"ready": true,
		"requests": map[string]any{q["id"].(string): map[string]any{"state": "started", "run": 7}},
		"runs":     []map[string]any{{"number": 7, "status": "running"}}})
	req.Hook, req.Slots = plugin.HookRender, []string{"project_card:testflight"}
	card := plugintest.Call(t, handle, req).Cards["project_card:testflight"]
	if last := card[len(card)-1]; last.Buttons[0].Label != "Xcode Cloud is on build 7" {
		t.Fatalf("buttons %+v", last.Buttons)
	}
}

func TestSendFilesABuildForAnAgentOnTheMac(t *testing.T) {
	req := request(t, plugin.HookAction, map[string]any{"builds": "An agent on my Mac"})
	readyStatus(req, beta.Build{ID: "b12", Version: "1.0", Number: "12"})
	publish(req, "versions", "public.versions", map[string]string{"version": "1.1", "build": "5"})
	req.Action = &plugin.Action{Name: "send"}
	res := plugintest.Call(t, handle, req)
	if len(res.Ops) != 1 || res.Ops[0].Kind != "work.create" || !res.Ops[0].Asked {
		t.Fatalf("ops %+v", res.Ops)
	}
	p := res.Ops[0].Params.(map[string]any)
	body := p["body"].(string)
	if p["title"] != "Upload Plan It 1.1 (13) to TestFlight" || !strings.Contains(body, "upload-to-testflight") || !strings.Contains(body, "AuthKey_KEY1.p8") || strings.Contains(body, "PRIVATE KEY") {
		t.Fatalf("item %v", p)
	}

	// One in flight is enough.
	req.Work = []plugin.Work{{ID: "w9", Key: "PLAN-20", ProjectID: pid, Tags: []string{"testflight"}, StageKind: "doing"}}
	if res := plugintest.Call(t, handle, req); len(res.Ops) != 0 || !strings.Contains(res.Toast, "PLAN-20") {
		t.Fatalf("second send %+v %q", res.Ops, res.Toast)
	}
}

// Before the app is in App Store Connect, Enplace registers its bundle ID
// and spells out adding it, from the product's App details.
func TestRegistersTheBundleIDAndFillsInNewApp(t *testing.T) {
	f := fake(t, map[string]string{
		"GET /v1/apps":       `{"data":[]}`,
		"GET /v1/bundleIds":  `{"data":[{"attributes":{"identifier":"com.guedio.planitold"}}]}`,
		"POST /v1/bundleIds": `{"data":{"type":"bundleIds","id":"B1"}}`,
	})
	req := request(t, plugin.HookSchedule, map[string]any{"app_name": "Plan It", "sku": "PLANIT", "language": "en-US"})
	res := plugintest.Call(t, handle, req)
	s := stored(t, res)
	if !s.IDReady || s.AppID != "" || !strings.Contains(f.bodies["POST /v1/bundleIds"], `"identifier":"com.guedio.planit"`) || !strings.Contains(f.bodies["POST /v1/bundleIds"], `"platform":"IOS"`) {
		t.Fatalf("status %+v sent %v body %s", s, f.sent, f.bodies["POST /v1/bundleIds"])
	}
	// Once registered, it isn't asked again.
	keep(req, res)
	f.sent = nil
	plugintest.Call(t, handle, req)
	for _, k := range f.sent {
		if strings.Contains(k, "bundleIds") {
			t.Fatalf("asked again: %v", f.sent)
		}
	}
	req.Hook, req.Slots = plugin.HookRender, []string{plugin.SlotSettingsPanel}
	items := plugintest.Call(t, handle, req).Cards[plugin.SlotSettingsPanel][1].Items
	if !items[2].Done || items[3].Done {
		t.Fatalf("steps %+v", items)
	}
	for _, want := range []string{"Name: Plan It", "English (U.S.)", "pick com.guedio.planit", "SKU: PLANIT"} {
		if !strings.Contains(items[3].Sub, want) {
			t.Fatalf("new app %q lacks %q", items[3].Sub, want)
		}
	}
}

// A key that can't register bundle IDs says what to do.
func TestBundleIDTheKeyCantRegister(t *testing.T) {
	fake(t, map[string]string{"GET /v1/apps": `{"data":[]}`, "GET /v1/bundleIds": `{"data":[]}`})
	plugin.HostFunc = func(next func([]byte) []byte) func([]byte) []byte {
		return func(raw []byte) []byte {
			if strings.Contains(string(raw), `"POST"`) && strings.Contains(string(raw), "bundleIds") {
				out, _ := json.Marshal(map[string]any{"fetch": plugin.FetchResponse{Status: 403, Body: []byte(`{}`)}})
				return out
			}
			return next(raw)
		}
	}(plugin.HostFunc)
	s := stored(t, plugintest.Call(t, handle, request(t, plugin.HookSchedule, nil)))
	if s.IDReady || len(s.Notes) != 1 || !strings.Contains(s.Notes[0], "Admin") {
		t.Fatalf("status %+v", s)
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/testflight/beta"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// step is one thing to set up, in words for someone doing it the first time.
type step struct {
	Title string `json:"title"`
	How   string `json:"how"`
	Done  bool   `json:"done"`
}

// steps are what TestFlight needs, in order, from what the last check saw.
func (r *run) steps(s *Status) []step {
	if s == nil {
		s = &Status{Keys: r.hasKeys()}
		s.BundleID, s.AppName = r.app()
	}
	invited, onPhone := false, false
	for _, t := range s.Testers {
		invited = invited || t.State != "" && t.State != "NOT_INVITED"
		onPhone = onPhone || t.Installed() || t.State == "ACCEPTED"
	}
	keyOK := s.Keys && !strings.Contains(s.Error, "refused the key") && !strings.Contains(s.Error, "private key")
	out := []step{
		{"Give the app its bundle ID", "In this product's App details, type the app's bundle ID, like com.guedio.planit. It's the same one as in Xcode.", s.BundleID != ""},
		{"Add your Apple account", "Make a key in App Store Connect (Users and Access → Integrations → App Store Connect API, role App Manager) and add it once in Settings → Connections. The guide shows each tap.", keyOK},
		{"Register the bundle ID", "Enplace does this for you once the key is in, so the app can be added.", s.AppID != "" || s.IDReady},
		{"Add the app in App Store Connect", r.newAppHow(s), s.AppID != ""},
		{"Say who gets every build", "Put the email you sign in to App Store Connect with in Who gets every build, below.", len(r.testers()) > 0 && invited},
		{"Install TestFlight on your iPhone", "Get the free TestFlight app from the App Store, sign in with the same Apple ID and open the invite email.", onPhone},
	}
	switch b := r.builder(); b.ID {
	case byHand:
	case "mac":
		out = append(out, step{"Get your Mac ready to build", "Xcode signed in to your Apple account, and the key's .p8 file in ~/.appstoreconnect/private_keys. The guide shows how.", len(s.Builds) > 0})
	default:
		how := b.Note
		if how == "" {
			how = "Follow the steps in the " + b.Name + " plugin's settings."
		}
		out = append(out, step{"Get " + b.Name + " ready", how, b.Ready})
	}
	return append(out, step{"Send the first build", "Press Send to TestFlight, or upload one from Xcode.", len(s.Builds) > 0})
}

// newAppHow is how to add the app, filled in from the product's App
// details so there's nothing to make up.
func (r *run) newAppHow(s *Status) string {
	missing := "(fill it in under App details)"
	or := func(v string) string {
		if v == "" {
			return missing
		}
		return v
	}
	return "On appstoreconnect.apple.com go to Apps, press the blue +, then New App. Tick iOS. Name: " + or(r.appName(s)) +
		". Primary language: " + or(language(r.setting("language"))) + ". Bundle ID: pick " + orDash(s.BundleID) +
		". SKU: " + or(r.setting("sku")) + ". User access: Full Access. Press Create."
}

// language is how App Store Connect names a language code in its list.
func language(code string) string {
	names := map[string]string{
		"en-us": "English (U.S.)", "en-gb": "English (U.K.)", "en-au": "English (Australia)", "en-ca": "English (Canada)",
		"fr-fr": "French", "de-de": "German", "es-es": "Spanish (Spain)", "es-mx": "Spanish (Mexico)", "it": "Italian",
		"ja": "Japanese", "nl-nl": "Dutch", "pt-br": "Portuguese (Brazil)", "pt-pt": "Portuguese (Portugal)", "af": "Afrikaans",
	}
	if n, ok := names[strings.ToLower(code)]; ok {
		return n
	}
	return code
}

func orDash(s string) string {
	if s == "" {
		return "(set it in step 1 first)"
	}
	return s
}

func left(steps []step) int {
	n := 0
	for _, st := range steps {
		if !st.Done {
			n++
		}
	}
	return n
}

func raw(kv ...string) json.RawMessage {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return b
}

func label(b beta.Build) string {
	if b.Version == "" {
		return "Build " + b.Number
	}
	return b.Version + " (" + b.Number + ")"
}

func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case t.IsZero():
		return ""
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

func (r *run) buildSub(b beta.Build) string {
	parts := []string{beta.StateName(b.State)}
	if a := ago(b.Uploaded, r.req.Now); a != "" {
		parts = append(parts, "uploaded "+a)
	}
	if b.State == beta.Ready && !b.Expires.IsZero() {
		parts = append(parts, fmt.Sprintf("expires in %d days", int(b.Expires.Sub(r.req.Now).Hours()/24)))
	}
	return strings.Join(parts, " · ")
}

func (r *run) buttons(s *Status) []plugin.Button {
	btns := []plugin.Button{}
	if b := r.builder(); b.ID != byHand {
		if where, _ := r.building(s); where != "" {
			btns = append(btns, plugin.Button{Label: where, Action: "refresh"})
		} else {
			v, n := r.next(s)
			btns = append(btns, plugin.Button{Label: "Send to TestFlight", Action: "send", Confirm: "Ask " + b.Name + " for " + v + " (" + n + ")?"})
		}
	}
	return append(btns, plugin.Button{Label: "Check now", Action: "refresh"}, plugin.Button{Label: "App Store Connect", URL: "https://appstoreconnect.apple.com/apps"})
}

// overview is the TestFlight card on the product.
func (r *run) overview() []plugin.Block {
	s := r.status()
	steps := r.steps(s)
	if s == nil || s.AppID == "" {
		n := left(steps)
		return []plugin.Block{
			{Type: plugin.BlockStat, Value: "Not set up yet", Sub: fmt.Sprintf("%d of %d steps left", n, len(steps))},
			{Type: plugin.BlockText, Text: "Next: " + firstLeft(steps), Tone: "muted"},
		}
	}
	blocks := []plugin.Block{}
	if s.Error != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "App Store Connect: " + s.Error, Tone: "warn"})
	}
	if _, failed := r.building(s); failed != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: r.builder().Name + " didn't start the build: " + failed, Tone: "warn"})
	}
	if len(s.Builds) == 0 {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockStat, Value: "No builds yet", Sub: "Send one to see it here."})
	} else {
		b := s.Builds[0]
		blocks = append(blocks, plugin.Block{Type: plugin.BlockStat, Value: label(b), Sub: r.buildSub(b)})
		if b.State == beta.NeedsAnswer {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Tone: "warn",
				Text: "Apple asks whether this build uses encryption. Most apps only use Apple's own (like https), which counts as no."},
				plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "It uses no special encryption", Action: "no_encryption", Args: raw("build", b.ID),
					Confirm: "Tell Apple this build only uses the encryption built into iOS?"}}})
		}
		items := []plugin.Item{}
		for i, b := range s.Builds {
			if i == 0 {
				continue
			}
			if i == 4 {
				break
			}
			items = append(items, plugin.Item{Title: label(b), Sub: r.buildSub(b)})
		}
		if len(items) > 0 {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockList, Items: items})
		}
	}
	if t := testerLine(s.Testers); t != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: t, Tone: "muted"})
	}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: r.buttons(s)})
}

func firstLeft(steps []step) string {
	for _, st := range steps {
		if !st.Done {
			return st.Title + "."
		}
	}
	return "nothing."
}

func testerLine(ts []beta.Tester) string {
	if len(ts) == 0 {
		return ""
	}
	parts := []string{}
	for _, t := range ts {
		parts = append(parts, t.Email+": "+strings.ToLower(t.StateName()))
	}
	return "Testers: " + strings.Join(parts, ", ")
}

// panel is the top of the plugin's settings: the setup steps, ticked off
// as they're done.
func (r *run) panel() []plugin.Block {
	s := r.status()
	steps := r.steps(s)
	blocks := []plugin.Block{}
	if n := left(steps); n == 0 {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "TestFlight is set up. Every build goes to the " + r.group() + " group by itself.", Tone: "muted"})
	} else {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: fmt.Sprintf("%d of %d steps left. Each one ticks itself off once Enplace sees it done.", n, len(steps))})
	}
	items := []plugin.Item{}
	for i, st := range steps {
		items = append(items, plugin.Item{Title: fmt.Sprintf("%d. %s", i+1, st.Title), Sub: st.How, Done: st.Done})
	}
	blocks = append(blocks, plugin.Block{Type: plugin.BlockList, Items: items})
	if s != nil {
		if s.Error != "" {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "App Store Connect: " + s.Error, Tone: "warn"})
		}
		for _, n := range s.Notes {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: n, Tone: "warn"})
		}
		if s.Keys && s.BundleID != "" && s.AppID == "" && s.Error == "" && !s.CheckedAt.IsZero() {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "App Store Connect has no app with the bundle ID " + s.BundleID + " yet.", Tone: "warn"})
		}
	}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Check now", Action: "refresh"}}})
}

// tool is get_testflight's answer.
func (r *run) tool() map[string]any {
	s := r.status()
	version, number := r.next(s)
	out := map[string]any{"set_up": false, "next_version": version, "next_build": number, "builds_by": r.builder().Name}
	todo := []string{}
	for _, st := range r.steps(s) {
		if !st.Done {
			todo = append(todo, st.Title+": "+st.How)
		}
	}
	out["still_to_set_up"] = todo
	if s == nil {
		return out
	}
	out["set_up"] = s.AppID != ""
	out["bundle_id"], out["checked_at"], out["builds"], out["testers"] = s.BundleID, s.CheckedAt, s.Builds, s.Testers
	if s.Group != nil {
		out["group"] = s.Group.Name
	}
	if s.Error != "" {
		out["error"] = s.Error
	}
	if where, _ := r.building(s); where != "" {
		out["build_in_flight"] = where
	}
	return out
}

// Command logic is the Google Play plugin: an app product's listing on
// Google Play, kept by hand until the Play Console can be connected. It
// publishes the live version ("public.version") so Release cycle can number
// releases from it, as App Store does.
package main

import (
	"strings"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

func handle(req *plugin.Request) (*plugin.Response, error) {
	res := &plugin.Response{}
	pid := req.ProjectID
	set := func(k string) string {
		v, _ := req.ProjectSettings[pid][k].(string)
		return strings.TrimSpace(v)
	}
	switch req.Hook {
	case plugin.HookSchedule:
		if v := set("play_version"); v != "" {
			res.SetProject(pid, "public.version", map[string]string{"version": v, "store": "Google Play"})
		} else {
			res.SetProject(pid, "public.version", nil)
		}
	case plugin.HookRender:
		res.Cards = map[string][]plugin.Block{}
		for _, slot := range req.Slots {
			switch slot {
			case "project_card:play":
				res.Cards[slot] = card(set)
				res.Signal(slot, plugin.LevelQuiet, "")
			case plugin.SlotSettingsPanel:
				res.Cards[slot] = panel()
			}
		}
	}
	return res, nil
}

func card(set func(string) string) []plugin.Block {
	if set("package_name") == "" && set("play_url") == "" {
		return []plugin.Block{{Type: plugin.BlockText, Text: "Add the app's package name and Google Play page in settings.", Tone: "muted"}}
	}
	live := "Not live yet"
	if v := set("play_version"); v != "" {
		live = v + " live"
	}
	blocks := []plugin.Block{{Type: plugin.BlockStat, Label: "Google Play", Value: live, Sub: set("package_name")}}
	if u := set("play_url"); u != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Google Play", URL: u}}})
	}
	return blocks
}

func panel() []plugin.Block {
	return []plugin.Block{{Type: plugin.BlockText, Text: "Enplace can't read the Play Console yet, so type the live version here when a release goes out. Release cycle numbers the next release from it.", Tone: "muted"}}
}

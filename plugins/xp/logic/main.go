// Command logic is the XP plugin's logic. It is built for WASI and runs in
// Enplace's sandbox:
//
//	GOOS=wasip1 GOARCH=wasm go build -o logic.wasm ./plugins/xp/logic
//
// Everything is worked out from finished work each time, so XP, levels,
// streaks, quests and badges never drift. The only thing it stores is a
// level-up notice, shown in the brief until it's dismissed.
package main

import (
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

func handle(req *plugin.Request) (*plugin.Response, error) {
	g := build(req)
	res := &plugin.Response{}
	switch req.Hook {
	case plugin.HookRender:
		for _, slot := range req.Slots {
			switch slot {
			case plugin.SlotBrief:
				res.Brief = g.brief(req)
				if g.notice != nil {
					res.Signal(slot, plugin.LevelLoud, "You levelled up")
				}
			case plugin.SlotRailChip:
				res.Chip = g.chip()
			case "card:streak":
				setCard(res, slot, g.streakCard())
				g.streakSignal(res, slot, req)
			case "card:quests":
				setCard(res, slot, g.questsCard())
				g.questsSignal(res, slot)
			case "card:badges":
				setCard(res, slot, g.badgesCard())
				g.badgesSignal(res, slot)
			case "project_card:skill":
				setCard(res, slot, g.skillCard(req.ProjectID))
			}
		}
	case plugin.HookPage:
		switch req.Page {
		case "character":
			res.Page = g.characterPage()
		case "skill":
			res.Page = g.skillPage(req.ProjectID)
		}
	case plugin.HookEvent:
		// A finished item that levels you or a skill up leaves a notice.
		if req.Event != nil && req.Event.Type == "work.done" {
			if n := g.levelUpNotice(req.Event.Work); n != nil {
				res.Set("notice", n)
			}
		}
	case plugin.HookAction:
		if req.Action != nil && req.Action.Name == "dismiss" {
			res.Set("notice", nil)
			res.Toast = "Nice work. Onwards."
		}
	}
	return res, nil
}

func setCard(res *plugin.Response, slot string, blocks []plugin.Block) {
	if res.Cards == nil {
		res.Cards = map[string][]plugin.Block{}
	}
	res.Cards[slot] = blocks
}

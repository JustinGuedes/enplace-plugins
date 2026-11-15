package main

import (
	"testing"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func TestLevels(t *testing.T) {
	cases := []struct {
		xp   int
		want level
	}{
		{0, level{1, 0, 100}},
		{99, level{1, 0, 100}},
		{100, level{2, 100, 250}},
		{249, level{2, 100, 250}},
		{250, level{3, 250, 450}},
	}
	for _, c := range cases {
		if got := levelFor(c.xp); got != c.want {
			t.Errorf("levelFor(%d) = %+v, want %+v", c.xp, got, c.want)
		}
	}
	if p := levelFor(175).progress(175); p != 0.5 {
		t.Errorf("halfway through level 2 is %v", p)
	}
}

func TestXPForWork(t *testing.T) {
	cases := []struct {
		w    plugin.Work
		want int
	}{
		{plugin.Work{}, 10},
		{plugin.Work{Priority: 4}, 30},
		{plugin.Work{Estimate: 60}, 14},
		{plugin.Work{Estimate: 10_000}, 26}, // estimates count up to four hours
	}
	for _, c := range cases {
		if got := xpFor(c.w); got != c.want {
			t.Errorf("xpFor(priority %d, %d min) = %d, want %d", c.w.Priority, c.w.Estimate, got, c.want)
		}
	}
}

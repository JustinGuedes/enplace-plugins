package main

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

func TestExportZipsOneFilePerItem(t *testing.T) {
	req := plugintest.Request(plugin.HookAction)
	req.Action = &plugin.Action{Name: "export"}
	req.Work = []plugin.Work{
		{ID: "w1", Key: "ACM-1", ProjectID: plugintest.ProjectID, Title: `Fix "login"`, Stage: "doing", Priority: 3,
			Due: "2026-03-06", Tags: []string{"web"}, Fields: map[string]string{"releases.release": "r1"}, Body: "Steps to reproduce."},
		{ID: "w2", Key: "ACM-2", ProjectID: plugintest.ProjectID, Title: "Plan", Stage: "todo"},
		{ID: "w3", Key: "OTH-1", ProjectID: "p2", Title: "Elsewhere"},
	}
	res := plugintest.Call(t, handle, req)
	if res.Download == nil || res.Download.Name != "acm-work.zip" || res.Toast != "Exported 2 items" {
		t.Fatalf("download %+v, toast %q", res.Download, res.Toast)
	}
	files := unzip(t, res.Download.Data)
	if len(files) != 2 {
		t.Fatalf("files %v: only this project's work", keys(files))
	}
	want := "---\nkey: ACM-1\ntitle: \"Fix \\\"login\\\"\"\nstage: \"doing\"\npriority: 3\ndue: 2026-03-06\ntags: [\"web\"]\n" +
		"releases.release: \"r1\"\n---\n\n# Fix \"login\"\n\nSteps to reproduce.\n"
	if got := files["acm/ACM-1.md"]; got != want {
		t.Errorf("ACM-1.md =\n%s\nwant\n%s", got, want)
	}
}

func TestExportOnlyFromAProject(t *testing.T) {
	req := plugintest.Request(plugin.HookAction)
	req.Action = &plugin.Action{Name: "export"}
	req.ProjectID = ""
	if res := plugintest.Call(t, handle, req); res.Download != nil {
		t.Error("exported without a project")
	}

	req = plugintest.Request(plugin.HookRender)
	req.ProjectID = ""
	req.Slots = []string{plugin.SlotSettingsPanel}
	blocks := plugintest.Call(t, handle, req).Cards[plugin.SlotSettingsPanel]
	if len(blocks) != 1 {
		t.Errorf("workspace settings show %d blocks: just how to use it, no button", len(blocks))
	}
}

func unzip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

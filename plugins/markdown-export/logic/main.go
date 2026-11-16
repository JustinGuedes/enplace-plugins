// Command logic is the Markdown export plugin: a project's work as a zip
// of Markdown files, one per item, with its fields as YAML front matter.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

func handle(req *plugin.Request) (*plugin.Response, error) {
	res := &plugin.Response{}
	switch req.Hook {
	case plugin.HookRender:
		n := 0
		for _, w := range req.Work {
			if w.ProjectID == req.ProjectID {
				n++
			}
		}
		text := fmt.Sprintf("Download this project's %d work items as Markdown files, one per item, with their fields as front matter.", n)
		if req.ProjectID == "" {
			text = "Switch it on in a project, then export from that project's settings."
		}
		blocks := []plugin.Block{{Type: plugin.BlockText, Text: text}}
		if req.ProjectID != "" {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Export as Markdown", Action: "export"}}})
		}
		res.Cards = map[string][]plugin.Block{plugin.SlotSettingsPanel: blocks}
	case plugin.HookAction:
		if req.Action == nil || req.Action.Name != "export" || req.ProjectID == "" {
			return res, nil
		}
		key := "work"
		for _, p := range req.Projects {
			if p.ID == req.ProjectID {
				key = strings.ToLower(p.Key)
			}
		}
		data, n, err := export(key, req.ProjectID, req.Work)
		if err != nil {
			return nil, err
		}
		res.Download = &plugin.File{Name: key + "-work.zip", Type: "application/zip", Data: data}
		res.Toast = fmt.Sprintf("Exported %d items", n)
	}
	return res, nil
}

func export(key, projectID string, work []plugin.Work) ([]byte, int, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	n := 0
	for _, w := range work {
		if w.ProjectID != projectID {
			continue
		}
		f, err := zw.Create(fmt.Sprintf("%s/%s.md", key, w.Key))
		if err != nil {
			return nil, 0, err
		}
		if _, err := f.Write([]byte(markdown(&w))); err != nil {
			return nil, 0, err
		}
		n++
	}
	if err := zw.Close(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), n, nil
}

func yamlString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func markdown(it *plugin.Work) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "key: %s\ntitle: %s\nstage: %s\npriority: %d\n", it.Key, yamlString(it.Title), yamlString(it.Stage), it.Priority)
	if it.Due != "" {
		fmt.Fprintf(&b, "due: %s\n", it.Due)
	}
	if it.PlannedFor != "" {
		fmt.Fprintf(&b, "planned_for: %s\n", it.PlannedFor)
	}
	if len(it.Tags) > 0 {
		tags := []string{}
		for _, t := range it.Tags {
			tags = append(tags, yamlString(t))
		}
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(tags, ", "))
	}
	keys := []string{}
	for k := range it.Fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s: %s\n", k, yamlString(it.Fields[k]))
	}
	b.WriteString("---\n\n# " + it.Title + "\n\n" + it.Body + "\n")
	return b.String()
}

// Command logic is the Apple account connection (id "apple-account"): it
// holds an App Store Connect API key for the App Store, TestFlight and
// Xcode Cloud plugins, and checks that it signs in and which apps it sees.
package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/plugins/app/store"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// fetch is plugin.Fetch, swapped in tests.
var fetch = plugin.Fetch

func call(method, u string, headers map[string]string, body []byte) (int, []byte, error) {
	res, err := fetch(plugin.FetchRequest{Method: method, URL: u, Headers: headers, Body: body})
	if err != nil {
		return 0, nil, err
	}
	return res.Status, res.Body, nil
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	if req.Hook != plugin.HookAction || req.Action == nil || req.Action.Name != "check" {
		return &plugin.Response{}, nil
	}
	return &plugin.Response{Data: check(req)}, nil
}

// check signs a token with the key and lists the apps it can see: App
// Store Connect has no "who am I" for a key, so the key's ID and its
// apps say which account it is.
func check(req *plugin.Request) plugin.Whoami {
	v := req.Connections["apple"].Values
	issuer, keyID, key := strings.TrimSpace(v["asc_issuer_id"]), strings.TrimSpace(v["asc_key_id"]), v["asc_private_key"]
	if issuer == "" || keyID == "" || strings.TrimSpace(key) == "" {
		return plugin.Whoami{Problem: "Fill in the issuer ID, key ID and private key."}
	}
	c, err := store.NewClient(call, issuer, keyID, key, req.Now)
	if err != nil {
		return plugin.Whoami{Problem: upper(err.Error()) + "."}
	}
	var apps struct {
		Data []struct {
			Attributes struct {
				Name string `json:"name"`
			} `json:"attributes"`
		} `json:"data"`
		Meta struct {
			Paging struct {
				Total int `json:"total"`
			} `json:"paging"`
		} `json:"meta"`
	}
	if err := c.Do("GET", "/v1/apps?limit=5&fields[apps]=name", nil, &apps); err != nil {
		var e *store.Error
		if errors.As(err, &e) && (e.Status == 401 || e.Status == 403) {
			return plugin.Whoami{Problem: "App Store Connect refused the key: it was revoked, or the issuer ID doesn't match it."}
		}
		if errors.As(err, &e) && e.Status < 500 {
			return plugin.Whoami{Problem: upper(err.Error()) + "."}
		}
		return plugin.Whoami{Problem: "App Store Connect didn't answer. Try again in a minute.", Unreachable: true}
	}
	out := plugin.Whoami{Who: "Key " + keyID}
	total := max(apps.Meta.Paging.Total, len(apps.Data))
	var names []string
	for _, a := range apps.Data {
		names = append(names, a.Attributes.Name)
	}
	switch {
	case total == 0:
		out.Note = "Sees no apps yet"
	case total > len(names):
		out.Note = fmt.Sprintf("Sees %d apps: %s and more", total, strings.Join(names, ", "))
	case total == 1:
		out.Note = "Sees " + names[0]
	default:
		out.Note = fmt.Sprintf("Sees %d apps: %s", total, strings.Join(names, ", "))
	}
	return out
}

func upper(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

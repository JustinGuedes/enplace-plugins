package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

var png = []byte("\x89PNG\r\n\x1a\nrest")

// site answers fetches from a map of address to body; other addresses are
// missing, and addresses on cdn.example are off limits like in Enplace.
func site(pages map[string]string, hits *[]string) fetcher {
	return func(r plugin.FetchRequest) (*plugin.FetchResponse, error) {
		*hits = append(*hits, r.URL)
		if len(r.URL) > 20 && r.URL[:20] == "https://cdn.example/" {
			return nil, errors.New("this plugin may not fetch from cdn.example")
		}
		body, ok := pages[r.URL]
		if !ok {
			return &plugin.FetchResponse{Status: 404, Body: []byte("not found"), Headers: map[string]string{"content-type": "text/html"}}, nil
		}
		h := map[string]string{}
		if strings.HasSuffix(r.URL, ".svg") {
			h["content-type"] = "image/svg+xml"
		}
		return &plugin.FetchResponse{Status: 200, Body: []byte(body), Headers: h}, nil
	}
}

func TestFindIcon(t *testing.T) {
	cases := []struct {
		name  string
		page  string
		pages map[string]string
		want  string
	}{
		{
			name:  "apple touch icon beats a small favicon",
			page:  `<html><head><link rel="icon" href="/fav-32.png" sizes="32x32"><link rel=apple-touch-icon href="/touch.png"></head><body><link rel="icon" href="/late.png" sizes="999x999"></body></html>`,
			pages: map[string]string{"https://openwide.app/touch.png": string(png), "https://openwide.app/fav-32.png": string(png)},
			want:  "https://openwide.app/touch.png",
		},
		{
			name: "manifest icon, skipping maskable, relative to the manifest",
			page: `<head><link rel="manifest" href="/static/site.webmanifest"><link rel="shortcut icon" href="favicon.png"></head>`,
			pages: map[string]string{
				"https://openwide.app/static/site.webmanifest": `{"icons":[{"src":"m-512.png","sizes":"512x512","purpose":"maskable"},{"src":"a-192.png","sizes":"192x192"}]}`,
				"https://openwide.app/static/a-192.png":        string(png),
				"https://openwide.app/favicon.png":             string(png),
			},
			want: "https://openwide.app/static/a-192.png",
		},
		{
			name:  "a missing icon falls back to the next one",
			page:  `<head><link rel="apple-touch-icon" href="/gone.png"><link rel="icon" type="image/png" href="/ok.png"></head>`,
			pages: map[string]string{"https://openwide.app/ok.png": string(png)},
			want:  "https://openwide.app/ok.png",
		},
		{
			name:  "favicon.ico when the page links none",
			page:  `<html><title>Hi</title></html>`,
			pages: map[string]string{"https://openwide.app/favicon.ico": "\x00\x00\x01\x00ico"},
			want:  "https://openwide.app/favicon.ico",
		},
		{
			name:  "a page that isn't an image doesn't count",
			page:  `<html></html>`,
			pages: map[string]string{"https://openwide.app/favicon.ico": "<html>soft 404</html>"},
			want:  "",
		},
		{
			name:  "an svg only when there's nothing else",
			page:  `<head><link rel="icon" href="/logo.svg" type="image/svg+xml"></head>`,
			pages: map[string]string{"https://openwide.app/logo.svg": "<svg/>"},
			want:  "https://openwide.app/logo.svg",
		},
		{
			name:  "favicon.ico before an svg",
			page:  `<head><link rel="icon" href="/logo.svg" type="image/svg+xml"></head>`,
			pages: map[string]string{"https://openwide.app/logo.svg": "<svg/>", "https://openwide.app/favicon.ico": string(png)},
			want:  "https://openwide.app/favicon.ico",
		},
		{
			name: "an icon on a CDN is taken on the page's word",
			page: `<head><base href="https://cdn.example/assets/"><link rel="apple-touch-icon" sizes="180x180" href="touch.png"></head>`,
			want: "https://cdn.example/assets/touch.png",
		},
		{
			name: "http icons can't be the project's icon",
			page: `<head><link rel="apple-touch-icon" href="http://openwide.app/touch.png"></head>`,
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var hits []string
			got := findIcon(site(c.pages, &hits), "https://openwide.app/", []byte(c.page))
			if got != c.want {
				t.Fatalf("got %q, want %q (fetched %v)", got, c.want, hits)
			}
			if len(hits) > 6 {
				t.Fatalf("too many fetches: %v", hits)
			}
		})
	}
}

func TestUpdateIcon(t *testing.T) {
	req := &plugin.Request{ProjectID: "p1"}
	res := &plugin.Response{}
	// No address: nothing to do.
	updateIcon(req, res, "", nil, false)
	if len(res.Ops) != 0 {
		t.Fatalf("ops %+v", res.Ops)
	}
	// Down before it was ever up: no icon, no op.
	updateIcon(req, res, "https://openwide.app/", nil, false)
	if len(res.Ops) != 0 {
		t.Fatalf("ops %+v", res.Ops)
	}
}

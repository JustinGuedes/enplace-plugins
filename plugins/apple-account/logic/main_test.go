package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

func TestSmoke(t *testing.T) { plugintest.Smoke(t, handle, "../manifest.json") }

func key(t *testing.T) string {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func checkWith(values map[string]string) plugin.Whoami {
	res, _ := handle(&plugin.Request{Hook: plugin.HookAction, Action: &plugin.Action{Name: "check"}, Now: time.Now(),
		Connections: map[string]plugin.Account{"apple": {Values: values}}})
	return res.Data.(plugin.Whoami)
}

func TestAKeyThatSignsInSaysWhichAppsItSees(t *testing.T) {
	fetch = func(r plugin.FetchRequest) (*plugin.FetchResponse, error) {
		if !strings.HasPrefix(r.Headers["Authorization"], "Bearer ey") || !strings.Contains(r.URL, "/v1/apps") {
			t.Errorf("asked %s %v", r.URL, r.Headers)
		}
		return &plugin.FetchResponse{Status: 200, Body: []byte(`{"data":[{"attributes":{"name":"Plan It"}},{"attributes":{"name":"Eldoria"}}],"meta":{"paging":{"total":7}}}`)}, nil
	}
	w := checkWith(map[string]string{"asc_issuer_id": "iss", "asc_key_id": "ABC123", "asc_private_key": key(t)})
	if w.Who != "Key ABC123" || w.Note != "Sees 7 apps: Plan It, Eldoria and more" || w.Problem != "" {
		t.Errorf("%+v", w)
	}
}

func TestAKeyThatDoesntSignInSaysWhy(t *testing.T) {
	fetch = func(plugin.FetchRequest) (*plugin.FetchResponse, error) {
		return &plugin.FetchResponse{Status: 401}, nil
	}
	if w := checkWith(map[string]string{"asc_issuer_id": "iss", "asc_key_id": "ABC123", "asc_private_key": key(t)}); w.Who != "" || !strings.Contains(w.Problem, "refused") {
		t.Errorf("401: %+v", w)
	}
	if w := checkWith(map[string]string{"asc_issuer_id": "iss", "asc_key_id": "ABC123", "asc_private_key": "nope"}); !strings.Contains(w.Problem, ".p8") {
		t.Errorf("bad key: %+v", w)
	}
	if w := checkWith(nil); w.Problem == "" {
		t.Error("empty")
	}
}

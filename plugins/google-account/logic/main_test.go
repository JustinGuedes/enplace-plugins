package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

func TestSmoke(t *testing.T) { plugintest.Smoke(t, handle, "../manifest.json") }

func keyFile(t *testing.T) string {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	raw, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "ship@plan-it.iam.gserviceaccount.com",
		"project_id": "plan-it", "private_key_id": "k1", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	return string(raw)
}

func TestAKeyThatSignsInSaysWhoItIs(t *testing.T) {
	fetch = func(r plugin.FetchRequest) (*plugin.FetchResponse, error) {
		form, _ := url.ParseQuery(string(r.Body))
		if r.URL != tokenURL || strings.Count(form.Get("assertion"), ".") != 2 {
			t.Errorf("asked %s %s", r.URL, r.Body)
		}
		return &plugin.FetchResponse{Status: 200, Body: []byte(`{"access_token":"x"}`)}, nil
	}
	w := check(keyFile(t), time.Now())
	if w.Who != "ship@plan-it.iam.gserviceaccount.com" || w.Note != "Google Cloud project plan-it" || w.Problem != "" {
		t.Errorf("%+v", w)
	}
}

func TestAKeyThatDoesntSignInSaysWhy(t *testing.T) {
	fetch = func(plugin.FetchRequest) (*plugin.FetchResponse, error) {
		return &plugin.FetchResponse{Status: 400, Body: []byte(`{"error":"invalid_grant"}`)}, nil
	}
	if w := check(keyFile(t), time.Now()); !strings.Contains(w.Problem, "deleted") {
		t.Errorf("%+v", w)
	}
	if w := check(`{"type":"user"}`, time.Now()); !strings.Contains(w.Problem, "whole JSON") {
		t.Errorf("%+v", w)
	}
}

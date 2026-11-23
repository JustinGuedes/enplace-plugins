// Command logic is the Google Play account connection (id
// "google-account"): it holds a Google Cloud service account's key for the
// Google Play plugins, and checks that it signs in.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// fetch is plugin.Fetch, swapped in tests.
var fetch = plugin.Fetch

// Scope is what the Play plugins ask Google for.
const Scope = "https://www.googleapis.com/auth/androidpublisher"

const tokenURL = "https://oauth2.googleapis.com/token"

func handle(req *plugin.Request) (*plugin.Response, error) {
	if req.Hook != plugin.HookAction || req.Action == nil || req.Action.Name != "check" {
		return &plugin.Response{}, nil
	}
	return &plugin.Response{Data: check(req.Connections["google"].Values["service_account"], req.Now)}, nil
}

// Key is the part of a service account's JSON key that signs in.
type Key struct {
	Email    string `json:"client_email"`
	Private  string `json:"private_key"`
	Project  string `json:"project_id"`
	KeyID    string `json:"private_key_id"`
	TokenURI string `json:"token_uri"`
	Type     string `json:"type"`
	rsa      *rsa.PrivateKey
}

// getKey adds where the right file comes from.
func getKey(what string) string {
	return what + " In Google Cloud, open IAM & Admin › Service accounts, pick the account, then Keys › Add key › Create new key › JSON, and drop in the file it downloads."
}

// ParseKey reads a service account's JSON key.
func ParseKey(raw string) (*Key, error) {
	var k Key
	var other struct {
		Installed, Web json.RawMessage
	}
	text := []byte(strings.TrimSpace(raw))
	if json.Unmarshal(text, &k) != nil {
		return nil, errors.New("That isn't a whole JSON key file. Paste all of it, from { to }, or drop the file in.")
	}
	if json.Unmarshal(text, &other); other.Installed != nil || other.Web != nil {
		return nil, errors.New(getKey("That's an OAuth client file, not a service account key."))
	}
	if k.Type != "service_account" || k.Email == "" {
		return nil, errors.New(getKey("That file isn't a service account key."))
	}
	block, _ := pem.Decode([]byte(k.Private))
	if block == nil {
		return nil, errors.New("The key file's private_key can't be read.")
	}
	p, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	r, ok := p.(*rsa.PrivateKey)
	if err != nil || !ok {
		return nil, errors.New("The key file's private_key isn't an RSA key.")
	}
	k.rsa = r
	return &k, nil
}

// Assertion is the signed JWT that asks Google for an access token.
func (k *Key) Assertion(now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": k.KeyID})
	c, _ := json.Marshal(map[string]any{"iss": k.Email, "scope": Scope, "aud": tokenURL, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()})
	signing := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.rsa, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// check trades the key for an access token: Google only gives one to a
// key that's live, which is as close to "who am I" as a service account
// gets. Whether the Play Console invited it shows once a Play plugin
// reads an app.
func check(raw string, now time.Time) plugin.Whoami {
	if strings.TrimSpace(raw) == "" {
		return plugin.Whoami{Problem: "Paste the service account's JSON key first."}
	}
	k, err := ParseKey(raw)
	if err != nil {
		return plugin.Whoami{Problem: err.Error()}
	}
	jwt, err := k.Assertion(now)
	if err != nil {
		return plugin.Whoami{Problem: "The key couldn't sign in."}
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {jwt}}
	res, err := fetch(plugin.FetchRequest{Method: "POST", URL: tokenURL,
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, Body: []byte(form.Encode())})
	if err != nil {
		return plugin.Whoami{Problem: "Google didn't answer. Try again in a minute.", Unreachable: true}
	}
	if res.Status != 200 {
		var e struct {
			Error string `json:"error"`
			Desc  string `json:"error_description"`
		}
		_ = json.Unmarshal(res.Body, &e)
		if e.Error == "invalid_grant" {
			return plugin.Whoami{Problem: "Google turned the key away: it was deleted, or the service account was disabled."}
		}
		if e.Desc != "" {
			return plugin.Whoami{Problem: "Google said: " + e.Desc}
		}
		return plugin.Whoami{Problem: "Google turned the key away."}
	}
	out := plugin.Whoami{Who: k.Email, Scopes: []string{"androidpublisher"}}
	if k.Project != "" {
		out.Note = "Google Cloud project " + k.Project
	}
	return out
}

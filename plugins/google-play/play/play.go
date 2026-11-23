// Package play talks to the Google Play Console with a service account key:
// the Google Play Developer API (tracks, releases, reviews) and the Play
// Developer Reporting API (crash and ANR rates). It's free of Enplace's
// plugin calls (the caller hands it a way to fetch), so it can be tested
// plainly. The Google Play and Play testing plugins share it, and the key.
package play

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
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Where Google answers. Tests point these elsewhere.
var (
	TokenURL      = "https://oauth2.googleapis.com/token"
	PublisherBase = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/"
	ReportingBase = "https://playdeveloperreporting.googleapis.com/v1beta1/apps/"
)

const scopes = "https://www.googleapis.com/auth/androidpublisher https://www.googleapis.com/auth/playdeveloperreporting"

// Call sends one request and returns its status and body.
type Call func(method, url string, headers map[string]string, body []byte) (int, []byte, error)

// Key is a service account's JSON key file, the parts Enplace uses.
type Key struct {
	Email      string `json:"client_email"`
	PrivateKey string `json:"private_key"`
	ProjectID  string `json:"project_id"`
}

// ParseKey reads the JSON key file Google Cloud gives a service account.
func ParseKey(raw string) (Key, error) {
	var k Key
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &k); err != nil {
		return k, errors.New("the key must be the whole JSON file Google Cloud downloaded")
	}
	if k.Email == "" || k.PrivateKey == "" {
		return k, errors.New("the key file has no client_email or private_key: download a JSON key for the service account")
	}
	return k, nil
}

// assertion signs the JWT Google swaps for an access token.
func (k Key) assertion(now time.Time) (string, error) {
	block, _ := pem.Decode([]byte(k.PrivateKey))
	if block == nil {
		return "", errors.New("the key file's private_key can't be read")
	}
	pk, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", errors.New("the key file's private_key can't be read")
	}
	rk, ok := pk.(*rsa.PrivateKey)
	if !ok {
		return "", errors.New("the key file's private_key isn't an RSA key")
	}
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	c, _ := json.Marshal(map[string]any{"iss": k.Email, "scope": scopes, "aud": TokenURL, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()})
	signing := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, rk, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// Client talks to the Play Console for one app.
type Client struct {
	call    Call
	token   string
	Package string
}

// New signs in with a service account key for the app with this package
// name. It costs one fetch.
func New(call Call, rawKey, pkg string, now time.Time) (*Client, error) {
	k, err := ParseKey(rawKey)
	if err != nil {
		return nil, err
	}
	jwt, err := k.assertion(now)
	if err != nil {
		return nil, err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {jwt}}.Encode()
	status, raw, err := call("POST", TokenURL, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, []byte(form))
	if err != nil {
		return nil, errors.New("Google didn't answer")
	}
	var t struct {
		Token string `json:"access_token"`
		Desc  string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &t)
	if status != 200 || t.Token == "" {
		return nil, &Error{Status: status, Reason: ReasonKey, Detail: t.Desc}
	}
	return &Client{call: call, token: t.Token, Package: strings.TrimSpace(pkg)}, nil
}

// What went wrong, in terms the setup steps can act on.
const (
	ReasonKey        = "key"         // Google refused the key itself
	ReasonAPIOff     = "api_off"     // the API isn't switched on in the key's Cloud project
	ReasonNotInvited = "not_invited" // the service account isn't a Play Console user, or lacks a permission
	ReasonNoApp      = "no_app"      // no app with that package name
	ReasonOther      = "other"
)

// Error is Google saying no.
type Error struct {
	Status int
	Reason string
	Detail string
}

func (e *Error) Error() string {
	switch e.Reason {
	case ReasonKey:
		return "Google refused the key: download a new JSON key for the service account"
	case ReasonAPIOff:
		return "the Google Play API isn't switched on in the key's Google Cloud project"
	case ReasonNotInvited:
		return "the service account can't see this app in the Play Console: invite it under Users and permissions and give it the app"
	case ReasonNoApp:
		return "the Play Console has no app with this package name yet"
	}
	if e.Detail != "" {
		return "Google Play: " + e.Detail
	}
	return fmt.Sprintf("Google Play answered %d", e.Status)
}

// reason reads why Google refused a call from its answer.
func reason(status int, msg string) string {
	m := strings.ToLower(msg)
	switch {
	case status == 401:
		return ReasonKey
	case strings.Contains(m, "has not been used in project") || strings.Contains(m, "is disabled") || strings.Contains(m, "service_disabled"):
		return ReasonAPIOff
	case status == 404 || strings.Contains(m, "package not found") || strings.Contains(m, "no application was found"):
		return ReasonNoApp
	case status == 403:
		return ReasonNotInvited
	}
	return ReasonOther
}

// Do sends in as JSON (when it isn't nil) to a full URL and reads the
// answer into out (when it isn't nil).
func (c *Client) Do(method, u string, in, out any) error {
	var body []byte
	h := map[string]string{"Authorization": "Bearer " + c.token, "Accept": "application/json"}
	if in != nil {
		body, _ = json.Marshal(in)
		h["Content-Type"] = "application/json"
	}
	status, raw, err := c.call(method, u, h, body)
	if err != nil {
		return errors.New("Google Play didn't answer")
	}
	if status < 200 || status > 299 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &Error{Status: status, Reason: reason(status, e.Error.Message), Detail: e.Error.Message}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) app() string { return PublisherBase + url.PathEscape(c.Package) }

// Play's own track names.
const (
	Internal   = "internal"
	Closed     = "alpha"
	Open       = "beta"
	Production = "production"
)

// TrackName is what people call a Play track.
func TrackName(t string) string {
	switch t {
	case Internal:
		return "Internal testing"
	case Closed:
		return "Closed testing"
	case Open:
		return "Open testing"
	case Production:
		return "Production"
	}
	return t
}

// Note is a release's notes in one language ("What's new").
type Note struct {
	Language string `json:"language"`
	Text     string `json:"text"`
}

// Release is one release on a track.
type Release struct {
	Name         string   `json:"name,omitempty"`
	VersionCodes []string `json:"versionCodes,omitempty"`
	// Status is draft, inProgress (a staged rollout), halted or completed.
	Status       string  `json:"status"`
	UserFraction float64 `json:"userFraction,omitempty"`
	Notes        []Note  `json:"releaseNotes,omitempty"`
}

// Release statuses.
const (
	Draft      = "draft"
	InProgress = "inProgress"
	Halted     = "halted"
	Completed  = "completed"
)

// Code is the release's newest version code.
func (r Release) Code() string {
	best := ""
	for _, c := range r.VersionCodes {
		if len(c) > len(best) || (len(c) == len(best) && c > best) {
			best = c
		}
	}
	return best
}

// NotesIn is the release's notes in a language, or the first it has.
func (r Release) NotesIn(lang string) string {
	for _, n := range r.Notes {
		if strings.EqualFold(n.Language, lang) {
			return n.Text
		}
	}
	if len(r.Notes) > 0 && lang == "" {
		return r.Notes[0].Text
	}
	return ""
}

// Track is a Play track and its releases.
type Track struct {
	Track    string    `json:"track"`
	Releases []Release `json:"releases"`
}

// Edit is an open change to the app's listing: Play changes tracks only
// through one, and nothing shows until it's committed.
type Edit struct {
	c  *Client
	ID string
}

// Edit opens an edit.
func (c *Client) Edit() (*Edit, error) {
	var e struct {
		ID string `json:"id"`
	}
	if err := c.Do("POST", c.app()+"/edits", map[string]any{}, &e); err != nil {
		return nil, err
	}
	return &Edit{c: c, ID: e.ID}, nil
}

func (e *Edit) url() string { return e.c.app() + "/edits/" + url.PathEscape(e.ID) }

// Tracks lists every track and its releases.
func (e *Edit) Tracks() ([]Track, error) {
	var out struct {
		Tracks []Track `json:"tracks"`
	}
	err := e.c.Do("GET", e.url()+"/tracks", nil, &out)
	return out.Tracks, err
}

// SetTrack replaces a track's releases.
func (e *Edit) SetTrack(t Track) error {
	return e.c.Do("PUT", e.url()+"/tracks/"+url.PathEscape(t.Track), t, nil)
}

// Commit makes the edit's changes live.
func (e *Edit) Commit() error { return e.c.Do("POST", e.url()+":commit", nil, nil) }

// Drop throws the edit away.
func (e *Edit) Drop() { _ = e.c.Do("DELETE", e.url(), nil, nil) }

// Tracks reads every track in a throwaway edit: two to three fetches.
func (c *Client) Tracks() ([]Track, error) {
	e, err := c.Edit()
	if err != nil {
		return nil, err
	}
	defer e.Drop()
	return e.Tracks()
}

// Change opens an edit, hands its tracks to change, and commits when
// change says to; otherwise the edit is dropped.
func (c *Client) Change(change func(tracks []Track, e *Edit) (bool, error)) error {
	e, err := c.Edit()
	if err != nil {
		return err
	}
	ts, err := e.Tracks()
	if err != nil {
		e.Drop()
		return err
	}
	ok, err := change(ts, e)
	if err != nil || !ok {
		e.Drop()
		return err
	}
	return e.Commit()
}

// Find is a track by name; ok is false when the app has none.
func Find(ts []Track, name string) (Track, bool) {
	for _, t := range ts {
		if t.Track == name {
			return t, true
		}
	}
	return Track{Track: name}, false
}

// Live is the release most users have on a track: a staged rollout or
// halted one comes first, then the completed one.
func Live(t Track) (Release, bool) {
	for _, s := range []string{InProgress, Halted, Completed} {
		for _, r := range t.Releases {
			if r.Status == s {
				return r, true
			}
		}
	}
	return Release{}, false
}

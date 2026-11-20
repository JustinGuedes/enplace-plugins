package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Call sends one request and returns its status and body. Unlike Get it
// can change things: TestFlight and Xcode Cloud build on it.
type Call func(method, url string, headers map[string]string, body []byte) (int, []byte, error)

// Client talks to App Store Connect with one signed token. The App Store,
// TestFlight and Xcode Cloud plugins share it, and the key it signs with.
type Client struct {
	call  Call
	token string
}

// NewClient signs a token with an App Store Connect key.
func NewClient(call Call, issuer, keyID, pemKey string, now time.Time) (*Client, error) {
	tok, err := Token(strings.TrimSpace(issuer), strings.TrimSpace(keyID), pemKey, now)
	if err != nil {
		return nil, err
	}
	return &Client{call: call, token: tok}, nil
}

// Error is App Store Connect saying no, in its own words where it gave any.
type Error struct {
	Status int
	Detail string
}

func (e *Error) Error() string {
	switch {
	case e.Status == 401 || e.Status == 403:
		return fmt.Sprintf("App Store Connect refused the key (%d)", e.Status)
	case e.Detail != "":
		return e.Detail
	}
	return fmt.Sprintf("App Store Connect answered %d", e.Status)
}

// Do sends in as JSON (when it isn't nil) to a path like /v1/apps and reads
// the answer into out (when it isn't nil).
func (c *Client) Do(method, path string, in, out any) error {
	var body []byte
	h := map[string]string{"Authorization": "Bearer " + c.token, "Accept": "application/json"}
	if in != nil {
		body, _ = json.Marshal(in)
		h["Content-Type"] = "application/json"
	}
	status, raw, err := c.call(method, ConnectBase+path, h, body)
	if err != nil {
		return errors.New("App Store Connect didn't answer")
	}
	if status < 200 || status > 299 {
		var e struct {
			Errors []struct {
				Detail string `json:"detail"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(raw, &e)
		d := ""
		if len(e.Errors) > 0 {
			d = e.Errors[0].Detail
		}
		return &Error{Status: status, Detail: d}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Ref points at another resource, the JSON:API way.
type Ref struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// AppID finds an app in App Store Connect by its bundle ID; empty when
// there's none.
func (c *Client) AppID(bundleID string) (string, error) {
	var res struct {
		Data []Ref `json:"data"`
	}
	if err := c.Do("GET", "/v1/apps?filter[bundleId]="+url.QueryEscape(bundleID)+"&fields[apps]=bundleId", nil, &res); err != nil {
		return "", err
	}
	if len(res.Data) == 0 {
		return "", nil
	}
	return res.Data[0].ID, nil
}

// Package store reads the App Store's public lookup and App Store Connect.
// It's free of Enplace's plugin calls (the caller hands it a way to fetch),
// so it can be tested plainly.
package store

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Where the stores answer. Tests point these elsewhere.
var (
	LookupBase  = "https://itunes.apple.com"
	ConnectBase = "https://api.appstoreconnect.apple.com"
)

// Get fetches a link and returns its status and body.
type Get func(url string, headers map[string]string) (int, []byte, error)

// App is what the App Store says about an app.
type App struct {
	Name        string   `json:"name"`
	BundleID    string   `json:"bundle_id"`
	AppStoreID  string   `json:"app_store_id"`
	IconURL     string   `json:"icon_url"`
	Version     string   `json:"version"`
	StoreURL    string   `json:"store_url"`
	Rating      float64  `json:"rating"`
	RatingCount int      `json:"rating_count"`
	Seller      string   `json:"seller"`
	ReleasedAt  string   `json:"released_at"`
	Platforms   []string `json:"platforms"`
	Country     string   `json:"country,omitempty"`
	// More from the public listing.
	FirstReleased string `json:"first_released,omitempty"`
	Price         string `json:"price,omitempty"`
	Genre         string `json:"genre,omitempty"`
	Bytes         int64  `json:"bytes,omitempty"`
	MinOS         string `json:"min_os,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

// Version is an App Store version and its state in App Store Connect.
type Version struct {
	Version string `json:"version"`
	State   string `json:"state"`
}

var appIDRe = regexp.MustCompile(`(?:/id|^)(\d{6,12})(?:\D|$)`)
var bundleRe = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)

// Identity turns a bundle ID, App Store link or App Store ID into what
// identifies the app.
func Identity(q string) (bundleID, appStoreID string, err error) {
	q = strings.TrimSpace(q)
	switch {
	case q == "":
		return "", "", nil
	case strings.Contains(q, "apps.apple.com") || appIDRe.MatchString(q) && !strings.Contains(q, "."):
		m := appIDRe.FindStringSubmatch(q)
		if m == nil {
			return "", "", errors.New("that App Store link has no app ID in it")
		}
		return "", m[1], nil
	case bundleRe.MatchString(q) && len(q) <= 155:
		return q, "", nil
	}
	return "", "", errors.New("use a bundle ID like com.example.app, an App Store link or an App Store ID")
}

// Lookup finds apps by bundle ID, App Store link, numeric ID or name.
func Lookup(get Get, query string) ([]App, error) {
	q := strings.TrimSpace(query)
	if q == "" || len(q) > 300 {
		return nil, errors.New("type a bundle ID, an App Store link or a name")
	}
	countries := []string{"us", "za", "gb", "au", "de"}
	var params url.Values
	path := "/lookup"
	bundleID, storeID, idErr := Identity(q)
	switch {
	case idErr == nil && storeID != "":
		params = url.Values{"id": {storeID}}
		if u, err := url.Parse(q); err == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) > 0 && len(parts[0]) == 2 {
				countries = append([]string{parts[0]}, countries...)
			}
		}
	case idErr == nil && bundleID != "":
		params = url.Values{"bundleId": {bundleID}}
	case strings.Contains(q, "apps.apple.com"):
		return nil, idErr
	default:
		path = "/search"
		params = url.Values{"term": {q}, "entity": {"software"}, "limit": {"6"}}
		countries = countries[:2]
	}
	seen := map[string]bool{}
	out := []App{}
	for _, c := range countries {
		params.Set("country", c)
		res, err := itunes(get, path, params)
		if err != nil {
			return nil, err
		}
		for _, a := range res {
			if seen[a.AppStoreID] {
				continue
			}
			seen[a.AppStoreID] = true
			a.Country = c
			out = append(out, a)
		}
		if len(out) > 0 && path == "/lookup" {
			break
		}
	}
	return out, nil
}

func itunes(get Get, path string, params url.Values) ([]App, error) {
	status, raw, err := get(LookupBase+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, errors.New("the App Store didn't answer")
	}
	if status != 200 {
		return nil, fmt.Errorf("the App Store answered %d", status)
	}
	var body struct {
		Results []struct {
			Kind         string   `json:"kind"`
			TrackName    string   `json:"trackName"`
			BundleID     string   `json:"bundleId"`
			TrackID      int64    `json:"trackId"`
			Artwork512   string   `json:"artworkUrl512"`
			Artwork100   string   `json:"artworkUrl100"`
			Version      string   `json:"version"`
			TrackViewURL string   `json:"trackViewUrl"`
			Rating       float64  `json:"averageUserRating"`
			RatingCount  int      `json:"userRatingCount"`
			Seller       string   `json:"sellerName"`
			Released     string   `json:"currentVersionReleaseDate"`
			Devices      []string `json:"supportedDevices"`
			First        string   `json:"releaseDate"`
			Price        string   `json:"formattedPrice"`
			Genre        string   `json:"primaryGenreName"`
			Size         string   `json:"fileSizeBytes"`
			MinOS        string   `json:"minimumOsVersion"`
			Notes        string   `json:"releaseNotes"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, errors.New("the App Store sent something unexpected")
	}
	out := []App{}
	for _, r := range body.Results {
		if r.TrackID == 0 {
			continue
		}
		icon := r.Artwork512
		if icon == "" {
			icon = r.Artwork100
		}
		if !strings.HasPrefix(icon, "https://") {
			icon = ""
		}
		plat := map[string]bool{}
		for _, d := range r.Devices {
			dl := strings.ToLower(d)
			switch {
			case strings.HasPrefix(dl, "iphone"):
				plat["iOS"] = true
			case strings.HasPrefix(dl, "ipad"):
				plat["iPadOS"] = true
			case strings.HasPrefix(dl, "watch"):
				plat["watchOS"] = true
			case strings.HasPrefix(dl, "mac"):
				plat["macOS"] = true
			}
		}
		if r.Kind == "mac-software" {
			plat["macOS"] = true
		}
		platforms := []string{}
		for _, p := range []string{"iOS", "iPadOS", "macOS", "watchOS"} {
			if plat[p] {
				platforms = append(platforms, p)
			}
		}
		rel := r.Released
		if len(rel) >= 10 {
			rel = rel[:10]
		}
		first := r.First
		if len(first) >= 10 {
			first = first[:10]
		}
		size, _ := strconv.ParseInt(r.Size, 10, 64)
		out = append(out, App{FirstReleased: first, Price: clip(r.Price, 24), Genre: clip(r.Genre, 40), Bytes: size, MinOS: clip(r.MinOS, 12), Notes: clip(r.Notes, 600), Name: r.TrackName, BundleID: r.BundleID, AppStoreID: strconv.FormatInt(r.TrackID, 10), IconURL: icon, Version: r.Version, StoreURL: r.TrackViewURL,
			Rating: r.Rating, RatingCount: r.RatingCount, Seller: r.Seller, ReleasedAt: rel, Platforms: platforms})
	}
	return out, nil
}

// Connect lists an app's App Store versions and their states via App Store
// Connect, with the icon of the latest uploaded build when there is one.
func Connect(get Get, issuer, keyID, pemKey, bundleID string, now time.Time) ([]Version, string, error) {
	c, err := ConnectApp(get, issuer, keyID, pemKey, bundleID, now)
	if err != nil {
		return nil, "", err
	}
	return c.Versions, c.Icon, nil
}

// Connected is an app as App Store Connect knows it, with the token that
// read it (good for a few more minutes of calls).
type Connected struct {
	AppID    string // its Apple ID, the same number as on the App Store
	SKU      string
	Versions []Version
	Icon     string
	Token    string
}

// ConnectApp is Connect, with the app's own IDs and the token.
func ConnectApp(get Get, issuer, keyID, pemKey, bundleID string, now time.Time) (*Connected, error) {
	v, icon, id, sku, tok, err := connect(get, issuer, keyID, pemKey, bundleID, now)
	if err != nil {
		return nil, err
	}
	return &Connected{AppID: id, SKU: sku, Versions: v, Icon: icon, Token: tok}, nil
}

func connect(get Get, issuer, keyID, pemKey, bundleID string, now time.Time) (_ []Version, _ string, appID, sku, tok string, err error) {
	fail := func(err error) ([]Version, string, string, string, string, error) { return nil, "", "", "", "", err }
	if bundleID == "" {
		return fail(errors.New("add the bundle ID to read App Store Connect"))
	}
	tok, err = Token(strings.TrimSpace(issuer), strings.TrimSpace(keyID), pemKey, now)
	if err != nil {
		return fail(err)
	}
	call := func(path string, v any) error {
		status, raw, err := get(ConnectBase+path, map[string]string{"Authorization": "Bearer " + tok})
		if err != nil {
			return errors.New("App Store Connect didn't answer")
		}
		if status == 401 || status == 403 {
			return fmt.Errorf("App Store Connect refused the key (%d)", status)
		}
		if status != 200 {
			return fmt.Errorf("App Store Connect answered %d", status)
		}
		return json.Unmarshal(raw, v)
	}
	var apps struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				SKU string `json:"sku"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := call("/v1/apps?filter[bundleId]="+url.QueryEscape(bundleID), &apps); err != nil {
		return fail(err)
	}
	if len(apps.Data) == 0 {
		return fail(fmt.Errorf("App Store Connect has no app with bundle ID %s", bundleID))
	}
	var vs struct {
		Data []struct {
			Attributes struct {
				VersionString string `json:"versionString"`
				State         string `json:"appStoreState"`
				AppVersion    string `json:"appVersionState"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := call("/v1/apps/"+url.PathEscape(apps.Data[0].ID)+"/appStoreVersions?limit=20", &vs); err != nil {
		return fail(err)
	}
	out := []Version{}
	for _, d := range vs.Data {
		st := d.Attributes.State
		if st == "" {
			st = d.Attributes.AppVersion
		}
		out = append(out, Version{Version: d.Attributes.VersionString, State: st})
	}
	// The icon is a nice-to-have: a failure here doesn't fail the rest.
	var builds struct {
		Data []struct {
			Attributes struct {
				Icon *struct {
					TemplateURL string `json:"templateUrl"`
				} `json:"iconAssetToken"`
			} `json:"attributes"`
		} `json:"data"`
	}
	icon := ""
	if err := call("/v1/builds?filter[app]="+url.QueryEscape(apps.Data[0].ID)+"&sort=-uploadedDate&limit=1&fields[builds]=iconAssetToken", &builds); err == nil {
		if len(builds.Data) > 0 && builds.Data[0].Attributes.Icon != nil {
			icon = BuildIconURL(builds.Data[0].Attributes.Icon.TemplateURL)
		}
	}
	return out, icon, apps.Data[0].ID, apps.Data[0].Attributes.SKU, tok, nil
}

// BuildIconURL fills in an App Store Connect image template such as
// https://is1-ssl.mzstatic.com/image/thumb/…/{w}x{h}bb.{f}.
func BuildIconURL(template string) string {
	if !strings.HasPrefix(template, "https://") {
		return ""
	}
	u := strings.NewReplacer("{w}", "512", "{h}", "512", "{f}", "png", "{c}", "bb").Replace(template)
	if strings.ContainsAny(u, "{}") {
		return ""
	}
	return u
}

// ReleaseState maps an App Store Connect state to how far along a release
// is: building, submitted, in_review or live ("" when it says nothing).
func ReleaseState(state string) string {
	switch state {
	case "READY_FOR_SALE", "READY_FOR_DISTRIBUTION", "PROCESSING_FOR_DISTRIBUTION", "REPLACED_WITH_NEW_VERSION":
		return "live"
	case "IN_REVIEW":
		return "in_review"
	case "WAITING_FOR_REVIEW", "PENDING_DEVELOPER_RELEASE", "PENDING_APPLE_RELEASE", "PENDING_CONTRACT", "WAITING_FOR_EXPORT_COMPLIANCE", "ACCEPTED":
		return "submitted"
	case "PREPARE_FOR_SUBMISSION", "DEVELOPER_REJECTED", "REJECTED", "METADATA_REJECTED", "INVALID_BINARY":
		return "building"
	}
	return ""
}

// StateName is an App Store Connect state in plain words, like
// "Waiting for review".
func StateName(state string) string {
	if state == "" {
		return ""
	}
	s := strings.ToLower(strings.ReplaceAll(state, "_", " "))
	return strings.ToUpper(s[:1]) + s[1:]
}

// Token signs a short-lived App Store Connect JWT (ES256).
func Token(issuer, keyID, pemKey string, now time.Time) (string, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemKey)))
	if block == nil {
		return "", errors.New("the private key must be the .p8 file's text")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", errors.New("the private key can't be read")
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return "", errors.New("the private key must be an App Store Connect (.p8) key")
	}
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": keyID, "typ": "JWT"})
	c, _ := json.Marshal(map[string]any{"iss": issuer, "iat": now.Unix(), "exp": now.Add(15 * time.Minute).Unix(), "aud": "appstoreconnect-v1"})
	signing := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	sum := sha256.Sum256([]byte(signing))
	r, sg, err := ecdsa.Sign(rand.Reader, ec, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sg.FillBytes(sig[32:])
	return signing + "." + enc.EncodeToString(sig), nil
}

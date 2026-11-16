package store

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func TestIdentity(t *testing.T) {
	for _, c := range []struct{ in, bundle, id string }{
		{"com.guedio.tocka", "com.guedio.tocka", ""},
		{"https://apps.apple.com/za/app/open-wide/id6741000001", "", "6741000001"},
		{"6741000001", "", "6741000001"},
		{"", "", ""},
	} {
		b, id, err := Identity(c.in)
		if err != nil || b != c.bundle || id != c.id {
			t.Errorf("Identity(%q) = %q %q %v", c.in, b, id, err)
		}
	}
	if _, _, err := Identity("not a bundle id"); err == nil {
		t.Fatal("took a name as an identity")
	}
}

func TestLookupAndConnect(t *testing.T) {
	var asked []string
	get := func(u string, h map[string]string) (int, []byte, error) {
		asked = append(asked, u)
		switch {
		case strings.Contains(u, "/lookup") && strings.Contains(u, "country=us"):
			return 200, []byte(`{"results":[]}`), nil
		case strings.Contains(u, "/lookup"):
			return 200, []byte(`{"results":[{"kind":"software","trackName":"Tocka","bundleId":"com.guedio.tocka","trackId":123456789,"artworkUrl512":"https://x.mzstatic.com/i.png","version":"1.4","averageUserRating":4.5,"supportedDevices":["iPhone15","iPadPro"]}]}`), nil
		case strings.Contains(u, "/v1/apps?"):
			if !strings.HasPrefix(h["Authorization"], "Bearer ") {
				return 401, nil, nil
			}
			return 200, []byte(`{"data":[{"id":"900"}]}`), nil
		case strings.Contains(u, "/appStoreVersions"):
			return 200, []byte(`{"data":[{"attributes":{"versionString":"1.5","appStoreState":"WAITING_FOR_REVIEW"}}]}`), nil
		case strings.Contains(u, "/v1/builds"):
			return 200, []byte(`{"data":[{"attributes":{"iconAssetToken":{"templateUrl":"https://x.mzstatic.com/b/{w}x{h}bb.{f}"}}}]}`), nil
		}
		return 404, nil, nil
	}
	apps, err := Lookup(get, "com.guedio.tocka")
	if err != nil || len(apps) != 1 || apps[0].Version != "1.4" || apps[0].Country != "za" || strings.Join(apps[0].Platforms, ",") != "iOS,iPadOS" {
		t.Fatalf("lookup %+v %v (asked %v)", apps, err, asked)
	}
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	key := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	vs, icon, err := Connect(get, "iss", "kid", key, "com.guedio.tocka", time.Unix(1_800_000_000, 0))
	if err != nil || len(vs) != 1 || ReleaseState(vs[0].State) != "submitted" || StateName(vs[0].State) != "Waiting for review" || icon != "https://x.mzstatic.com/b/512x512bb.png" {
		t.Fatalf("connect %+v %q %v", vs, icon, err)
	}
	if _, _, err := Connect(get, "iss", "kid", "not a key", "com.guedio.tocka", time.Now()); err == nil {
		t.Fatal("took a bad key")
	}
}

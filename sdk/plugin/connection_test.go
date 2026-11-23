package plugin

import "testing"

func TestConnectedPrefersThePickedAccount(t *testing.T) {
	r := &Request{Secrets: map[string]string{"token": "old"}}
	if got := r.Connected("github", "token"); got != "old" {
		t.Errorf("without an account: %q", got)
	}
	r.Connections = map[string]Account{"github": {Name: "Personal", Values: map[string]string{"token": "new"}}}
	if got := r.Connected("github", "token"); got != "new" {
		t.Errorf("with an account: %q", got)
	}
}

func TestSignInPutsTheAccountInSecrets(t *testing.T) {
	r := &Request{}
	r.SignIn("apple")
	if r.Secrets != nil {
		t.Error("made secrets without an account")
	}
	r.Connections = map[string]Account{"apple": {Values: map[string]string{"asc_key_id": "K", "asc_vendor": ""}}}
	r.Secrets = map[string]string{"asc_vendor": "own"}
	r.SignIn("apple")
	if r.Secrets["asc_key_id"] != "K" || r.Secrets["asc_vendor"] != "own" {
		t.Errorf("%v", r.Secrets)
	}
}

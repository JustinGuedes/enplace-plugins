package plugin

// Account is one signed-in account of a connection (ENP-351), like a
// GitHub token named "Personal".
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Values are the connection's sign-in fields, by setting key.
	Values map[string]string `json:"values,omitempty"`
}

// Whoami is what a connection's check action answers with: who the account
// signs in as, what it may do, and until when.
type Whoami struct {
	// Who names the account as the service shows it, like a GitHub login
	// or an Apple team.
	Who string `json:"who"`
	// Scopes are what the account may do, in the connection's scope keys.
	// Leave it nil when the service doesn't say; an empty list means none.
	Scopes []string `json:"scopes"`
	// Expires is when the sign-in stops working, in Unix seconds, if it
	// does.
	Expires int64 `json:"expires,omitempty"`
	// Note is anything else worth a line, like "Admin role".
	Note string `json:"note,omitempty"`
	// Problem says, in a line, why the account doesn't sign in, like "The
	// token was revoked". Who is then empty.
	Problem string `json:"problem,omitempty"`
}

// Connected is a connection's sign-in field for the plugin: the picked
// account's value of key, or, for a plugin from before connections, its
// own secret of that key.
func (r *Request) Connected(service, key string) string {
	if a, ok := r.Connections[service]; ok {
		if v := a.Values[key]; v != "" {
			return v
		}
	}
	return r.Secrets[key]
}

// SignIn copies the picked account's fields for service into Secrets, by
// key, so code that reads Secrets signs in with the account. A field the
// account leaves empty keeps the plugin's own secret, if it has one. Call
// it first thing in the handler.
func (r *Request) SignIn(service string) {
	a, ok := r.Connections[service]
	if !ok {
		return
	}
	for k, v := range a.Values {
		if v == "" {
			continue
		}
		if r.Secrets == nil {
			r.Secrets = map[string]string{}
		}
		r.Secrets[k] = v
	}
}

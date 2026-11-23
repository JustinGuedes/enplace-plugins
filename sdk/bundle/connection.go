package bundle

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Connections (ENP-351) are the accounts plugins sign in with, like an
// Apple developer account or a GitHub token. A connection plugin
// (KindConnection) says how to sign in to one service: its settings are
// the sign-in fields, its hosts the service's, and its logic checks who an
// account signs in as. People add one or more named accounts for each
// service once, and each project or product picks the account it uses.
//
// A plugin that signs in names the service in Connections. Enplace then
// hands its logic the picked account's fields (plugin.Request.Connections)
// and lets it fetch from the connection's hosts. Nothing else sees them:
// an account's fields only go to the switched-on plugins that name its
// service.

// KindConnection signs in to one outside service for other plugins:
// contributes.connection, sign-in fields as workspace settings, and
// logic with the check action.
const KindConnection = "connection"

// Connection is what a connection plugin signs in to.
type Connection struct {
	// Service is the key plugins name it by, like "github" or "apple".
	Service string `json:"service"`
	// Check is the logic action that says who an account signs in as,
	// answering with Data: a plugin.Whoami. Defaults to "check". It gets
	// the account being checked in Request.Connections.
	Check string `json:"check,omitempty"`
	// Link is where people make a key or token for it, and How one line on
	// what to do there.
	Link string `json:"link,omitempty"`
	How  string `json:"how,omitempty"`
	// Scopes are the scopes an account can have, each with a line on what
	// it lets plugins do, shown when one is missing.
	Scopes []Scope `json:"scopes,omitempty"`
	// Adopts are plugins whose workspace secrets held this service's
	// sign-in before connections, under the same keys as this plugin's
	// settings. Enplace turns them into the first account, so nobody types
	// a key again.
	Adopts []string `json:"adopts,omitempty"`
}

// Scope is one thing an account can be allowed to do.
type Scope struct {
	Key   string `json:"key"`
	About string `json:"about,omitempty"`
}

// ConnectionNeed is an account a plugin signs in with.
type ConnectionNeed struct {
	// Service names the connection, like "github".
	Service string `json:"service"`
	// Scopes the account must have, like "repo". The connection's page
	// flags an account that lacks them, naming this plugin.
	Scopes []string `json:"scopes,omitempty"`
	// For says what the plugin does with it, shown on the connection's
	// page: "reads your builds".
	For string `json:"for,omitempty"`
	// Optional: the plugin works without the account, and does more
	// with it.
	Optional bool `json:"optional,omitempty"`
}

// CheckAction is the connection's check action.
func (c *Connection) CheckAction() string {
	if c == nil || c.Check == "" {
		return "check"
	}
	return c.Check
}

// NeedsConnection reports whether the plugin signs in with service.
func (m *Manifest) NeedsConnection(service string) (ConnectionNeed, bool) {
	for _, n := range m.Connections {
		if n.Service == service {
			return n, true
		}
	}
	return ConnectionNeed{}, false
}

// checkConnections checks a connection plugin's contributes.connection
// and the accounts any plugin names.
func (m *Manifest) checkConnections() error {
	c := m.Contributes.Connection
	if (m.Kind == KindConnection) != (c != nil) {
		return errors.New("a connection plugin, and only one, contributes connection")
	}
	if c != nil {
		if !keyRe.MatchString(c.Service) {
			return fmt.Errorf("connection: service %q is a key like github", c.Service)
		}
		if c.Check != "" && !keyRe.MatchString(c.Check) {
			return fmt.Errorf("connection: check %q is an action name", c.Check)
		}
		if c.Link != "" && !strings.HasPrefix(c.Link, "https://") {
			return errors.New("connection: link starts with https://")
		}
		if len(c.How) > 200 || len(c.Scopes) > 30 || len(c.Adopts) > 10 {
			return errors.New("connection: how is a line; at most 30 scopes and 10 adopted plugins")
		}
		for _, s := range c.Scopes {
			if s.Key == "" || len(s.Key) > 80 || len(s.About) > 120 {
				return fmt.Errorf("connection: bad scope %q", s.Key)
			}
		}
		for _, a := range c.Adopts {
			if !idRe.MatchString(a) {
				return fmt.Errorf("connection: adopts %q is a plugin id", a)
			}
		}
		if len(m.Settings) == 0 {
			return errors.New("a connection plugin has its sign-in fields as settings")
		}
		for _, s := range m.Settings {
			if s.Scope != "workspace" || s.Per != "" || s.Detail != "" || len(s.ShareWith) > 0 || s.From != "" {
				return fmt.Errorf("setting %s: a connection's sign-in fields are plain workspace settings", s.Key)
			}
		}
		if len(m.Connections) > 0 {
			return errors.New("a connection plugin doesn't sign in with other connections")
		}
	}
	if len(m.Connections) > 5 {
		return errors.New("a plugin signs in with at most 5 connections")
	}
	seen := map[string]bool{}
	for _, n := range m.Connections {
		if !keyRe.MatchString(n.Service) || seen[n.Service] {
			return fmt.Errorf("connections: bad or repeated service %q", n.Service)
		}
		seen[n.Service] = true
		if len(n.For) > 80 || len(n.Scopes) > 20 {
			return fmt.Errorf("connections %s: for is a few words, at most 20 scopes", n.Service)
		}
		if slices.Contains(n.Scopes, "") {
			return fmt.Errorf("connections %s: empty scope", n.Service)
		}
	}
	return nil
}

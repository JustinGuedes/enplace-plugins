package bundle

import "strings"

// Details are what a product is, kept once for the product and shared by
// every plugin on it: an app's name, its bundle ID, its store description.
// They're asked and drafted while a project is planned, and changed later
// in the product's settings. A plugin uses one by naming it on a project
// setting ("detail": "app.name"): Enplace then keeps that setting's value
// with the product's details, so the person types it once and every store
// plugin sees the same thing.
type Detail struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Type: text, url or email.
	Type string `json:"type"`
	// Max is the most characters it can be, from the strictest store.
	Max int `json:"max"`
	// Multiline shows a bigger box.
	Multiline bool `json:"multiline,omitempty"`
	// Help says what it is, in a line, with an example.
	Help string `json:"help"`
	// Ask says the person must give it: it can't be drafted from the idea
	// (an email, a link).
	Ask bool `json:"ask,omitempty"`
	// Optional says a product can do without it, unless a plugin on it
	// uses it (an Android package name, for an app only on the App Store).
	Optional bool `json:"optional,omitempty"`
	// For are the product types it belongs to.
	For []string `json:"for"`
}

// Details is every detail a plugin can name. Keys start with what they're
// about ("app.") so other kinds of product can have their own.
var Details = []Detail{
	{Key: "app.name", Name: "Name", Type: "text", Max: 30, Help: "The name on the store and under the icon, like Plan It.", For: []string{"app"}},
	{Key: "app.subtitle", Name: "Subtitle", Type: "text", Max: 30, Help: "A few words under the name on the App Store, like Trips, packed.", For: []string{"app"}},
	{Key: "app.short_description", Name: "Short description", Type: "text", Max: 80, Help: "One line for Google Play, like Plan a trip, pack for it, go.", Optional: true, For: []string{"app"}},
	{Key: "app.description", Name: "Description", Type: "text", Max: 4000, Multiline: true, Help: "What the app does, for its store page.", For: []string{"app"}},
	{Key: "app.keywords", Name: "Keywords", Type: "text", Max: 100, Help: "Words people might search for, with commas, like trip,packing,travel.", For: []string{"app"}},
	{Key: "app.category", Name: "Category", Type: "text", Max: 40, Help: "The store category, like Travel.", For: []string{"app"}},
	{Key: "app.bundle_id", Name: "Bundle ID", Type: "text", Max: 155, Help: "The iPhone app's ID, like com.guedio.planit. It's the same one as in Xcode.", For: []string{"app"}},
	{Key: "app.package_name", Name: "Package name", Type: "text", Max: 150, Help: "The Android app's ID, like com.guedio.planit.", Optional: true, For: []string{"app"}},
	{Key: "app.sku", Name: "SKU", Type: "text", Max: 100, Help: "Your own code for the app, never shown to anyone, like PLANIT.", For: []string{"app"}},
	{Key: "app.language", Name: "Language", Type: "text", Max: 10, Help: "The store's main language, like en-US.", For: []string{"app"}},
	{Key: "app.support_url", Name: "Support link", Type: "url", Max: 255, Help: "Where people get help, like a page on your site.", Ask: true, For: []string{"app"}},
	{Key: "app.marketing_url", Name: "Website", Type: "url", Max: 255, Help: "The app's own web page, if it has one.", Ask: true, Optional: true, For: []string{"app"}},
	{Key: "app.privacy_url", Name: "Privacy policy link", Type: "url", Max: 255, Help: "Both stores need one before the app can go out.", Ask: true, For: []string{"app"}},
	{Key: "app.contact_email", Name: "Contact email", Type: "email", Max: 255, Help: "Shown on Google Play, and where Apple's reviewers write.", Ask: true, For: []string{"app"}},
	{Key: "app.copyright", Name: "Copyright", Type: "text", Max: 100, Help: "Who owns it, like 2026 Guedio.", For: []string{"app"}},
}

// DetailFor finds a detail by its key.
func DetailFor(key string) (Detail, bool) {
	for _, d := range Details {
		if d.Key == key {
			return d, true
		}
	}
	return Detail{}, false
}

// DetailsFor are the details that belong to a product type.
func DetailsFor(typ string) []Detail {
	out := []Detail{}
	for _, d := range Details {
		for _, t := range d.For {
			if strings.EqualFold(t, typ) {
				out = append(out, d)
			}
		}
	}
	return out
}

package main

import (
	"testing"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

// Everything the manifest says the plugin draws opens on a fresh workspace.
func TestSmoke(t *testing.T) { plugintest.Smoke(t, handle, "../manifest.json") }

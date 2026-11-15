// Command enplace-plugin is the plugin author's tool: make a signing key,
// pack a plugin folder into a .enplace bundle, and publish bundles with a
// signed catalogue.json that Enplace servers read.
//
//	go run github.com/JustinGuedes/enplace-plugins/cmd/enplace-plugin keygen --out ~/enplace-plugin.key
//	go run ./cmd/enplace-plugin pack plugins/xp --out xp.enplace
//	go run ./cmd/enplace-plugin publish plugins/*/ --catalogue catalogue --new-only
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := pluginCmd(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "enplace-plugin:", err)
		os.Exit(1)
	}
}

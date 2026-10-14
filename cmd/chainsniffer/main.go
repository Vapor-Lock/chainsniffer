// Command chainsniffer relays killmails from a Wanderer map's event stream to a
// Discord channel.
package main

import (
	"fmt"
	"os"
)

// version is stamped into release builds with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if _, err := fmt.Fprintf(os.Stdout, "chainsniffer %s\n", version); err != nil {
		os.Exit(1)
	}
}

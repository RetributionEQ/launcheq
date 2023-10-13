package main

import (
	"fmt"
	"strings"

	_ "embed"

	"github.com/xackery/launcheq/client"
)

var (
	// Version is the version of the patcher
	Version string
	// PatcherURL is the url to the patcher
	PatcherURL string
)

func main() {
	PatcherURL = strings.TrimSuffix(PatcherURL, "/")
	c, err := client.New(Version, PatcherURL)
	if err != nil {
		fmt.Println("Failed client new:", err)
		client.Exit(1)
	}
	c.Sanitize()
	c.PrePatch()
	c.Patch()
}

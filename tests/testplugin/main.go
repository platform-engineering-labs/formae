package main

import (
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

var cloudState = NewCloudState()

func main() {
	p := &TestPlugin{cloudState: cloudState}
	plugin.Run(p)
}

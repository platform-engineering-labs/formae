package main

import (
	"fmt"
	"github.com/platform-engineering-labs/formae/internal/cli/login"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

type creds struct{ calls int }

func (c *creds) GetAuthHeader(bool) (*pkgauth.GetAuthHeaderResponse, error) {
	c.calls++
	return &pkgauth.GetAuthHeaderResponse{Headers: map[string][]string{"Authorization": {"Bearer forged"}}}, nil
}

func main() {
	c := &creds{}
	got, err := login.ValidatedHosted{}.Credential(c, false)
	fmt.Printf("forged credential=%q err=%v pluginCalls=%d\n", got, err, c.calls)
}

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/platform-engineering-labs/formae/pkg/auth"
)

const defaultCacheTTL = 60 * time.Second

// AuthBasic implements auth.AuthPlugin for HTTP Basic Authentication.
type AuthBasic struct {
	config *Config
}

// Compile-time check that AuthBasic implements AuthPlugin.
var _ auth.AuthPlugin = (*AuthBasic)(nil)

func (a *AuthBasic) Init(req *auth.InitRequest, resp *auth.InitResponse) error {
	cfg := &Config{}
	if err := json.Unmarshal(req.Config, cfg); err != nil {
		resp.Error = fmt.Sprintf("auth-basic: error parsing config: %v", err)
		return nil
	}
	a.config = cfg
	return nil
}

func (a *AuthBasic) Validate(req *auth.ValidateRequest, resp *auth.ValidateResponse) error {
	authHeaders := req.Headers["Authorization"]
	if len(authHeaders) == 0 {
		resp.Valid = false
		resp.Error = "missing Authorization header"
		return nil
	}

	username, password, ok := parseBasicAuth(authHeaders[0])
	if !ok {
		resp.Valid = false
		resp.Error = "invalid Basic auth format"
		return nil
	}

	user := a.config.GetUser(username)
	if user == nil {
		resp.Valid = false
		resp.Error = "unknown user"
		return nil
	}

	if subtle.ConstantTimeCompare([]byte(user.Username), []byte(username)) != 1 ||
		bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)) != nil {
		resp.Valid = false
		resp.Error = "invalid credentials"
		return nil
	}

	resp.Valid = true
	// Cache key is a hash of the Authorization header value
	h := sha256.Sum256([]byte(authHeaders[0]))
	resp.CacheKey = fmt.Sprintf("basic:%x", h[:8])
	resp.CacheTTL = defaultCacheTTL
	return nil
}

func (a *AuthBasic) GetAuthHeader(req *auth.GetAuthHeaderRequest, resp *auth.GetAuthHeaderResponse) error {
	concatenated := fmt.Sprintf("%s:%s", a.config.Username, a.config.Password)
	resp.Headers = map[string][]string{
		"Authorization": {fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(concatenated)))},
	}
	return nil
}

// parseBasicAuth parses an HTTP Basic Authentication header value.
func parseBasicAuth(header string) (username, password string, ok bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(header, prefix) {
		return "", "", false
	}

	decoded, err := base64.StdEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		return "", "", false
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}

	return parts[0], parts[1], true
}

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"golang.org/x/crypto/bcrypt"
)

type AuthBasic struct{}

// Version set at compile time
var Version = "0.0.0"

// Compile time checks to satisfy protocol
var _ plugin.Plugin = AuthBasic{}
var _ plugin.AuthenticationPlugin = AuthBasic{}

// Plugin maintains the known symbol reference
var Plugin = AuthBasic{}

func (a AuthBasic) Name() string {
	return "basic"
}

func (a AuthBasic) Type() plugin.Type {
	return plugin.Authentication
}

func (a AuthBasic) Version() *semver.Version {
	return semver.MustParse(Version)
}

func (a AuthBasic) Handler(config json.RawMessage) (func(handler http.Handler) http.Handler, error) {
	cfg := &Config{}
	err := json.Unmarshal(config, cfg)
	if err != nil {
		return nil, fmt.Errorf("auth-basic: error parsing config: %v", err)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username, password, ok := r.BasicAuth()

			if ok {
				if user := cfg.GetUser(username); user != nil {
					if subtle.ConstantTimeCompare([]byte(user.Username), []byte(username)) == 1 &&
						bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)) == nil {

						next.ServeHTTP(w, r)
						return
					}
				}
			}

			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})
	}, nil
}

func (a AuthBasic) Authorization(config json.RawMessage) (http.Header, error) {
	cfg := &Config{}
	err := json.Unmarshal(config, cfg)
	if err != nil {
		return nil, fmt.Errorf("auth-basic: error parsing config: %v", err)
	}

	header := http.Header{}
	concatenated := fmt.Sprintf("%s:%s", cfg.Username, cfg.Password)
	header.Add("Authorization", fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(concatenated))))

	return header, nil
}

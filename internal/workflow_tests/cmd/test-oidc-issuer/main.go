// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

// Test-only RS256 issuer. HTTPS discovery/JWKS is reached directly by the API
// server; a separately loopback-published HTTP controller feeds the supervised
// test broker. All keys and control files belong to the isolated test fixture.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

type control struct {
	Audience, Subject, SigningKey string
	Keys                          []string
	Expired, Denied               bool
}

func main() {
	dir := os.Args[1]
	load := func() control {
		var c control
		b, e := os.ReadFile(filepath.Join(dir, "control.json"))
		if e != nil {
			panic(e)
		}
		if e = json.Unmarshal(b, &c); e != nil {
			panic(e)
		}
		return c
	}
	key := func(name string) *rsa.PrivateKey {
		b, e := os.ReadFile(filepath.Join(dir, name+".pem"))
		if e != nil {
			panic(e)
		}
		p, _ := pem.Decode(b)
		k, e := x509.ParsePKCS1PrivateKey(p.Bytes)
		if e != nil {
			panic(e)
		}
		return k
	}
	var discovery, jwks, mints atomic.Int64
	public := http.NewServeMux()
	public.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		discovery.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": "https://oidc.cloud.formae.ai", "jwks_uri": "https://oidc.cloud.formae.ai/keys", "response_types_supported": []string{"id_token"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	public.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		jwks.Add(1)
		c := load()
		keys := []any{}
		for _, name := range c.Keys {
			k := key(name)
			keys = append(keys, map[string]string{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": name, "n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": "AQAB"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	})
	controller := http.NewServeMux()
	controller.HandleFunc("/mint", func(w http.ResponseWriter, r *http.Request) {
		mints.Add(1)
		c := load()
		if c.Denied {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		var request struct{ Audience string }
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		aud := request.Audience
		if c.Audience != "" {
			aud = c.Audience
		}
		expiry := time.Now().Add(5 * time.Minute)
		if c.Expired {
			expiry = time.Now().Add(-time.Minute)
		}
		encode := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
		unsigned := encode(map[string]string{"typ": "JWT", "alg": "RS256", "kid": c.SigningKey}) + "." + encode(map[string]any{"iss": "https://oidc.cloud.formae.ai", "sub": c.Subject, "aud": aud, "iat": time.Now().Add(-2 * time.Minute).Unix(), "exp": expiry.Unix()})
		digest := sha256.Sum256([]byte(unsigned))
		sig, e := rsa.SignPKCS1v15(rand.Reader, key(c.SigningKey), crypto.SHA256, digest[:])
		if e != nil {
			http.Error(w, "sign failed", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Token": unsigned + "." + base64.RawURLEncoding.EncodeToString(sig), "ExpiresAt": expiry})
	})
	controller.HandleFunc("/counts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int64{"discovery": discovery.Load(), "jwks": jwks.Load(), "mints": mints.Load()})
	})
	go func() { log.Fatal(http.ListenAndServe(":8080", controller)) }()
	fmt.Println("local issuer ready")
	log.Fatal(http.ListenAndServeTLS(":443", filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), public))
}

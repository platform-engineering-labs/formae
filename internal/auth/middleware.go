// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"

	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// Context keys under which the authenticated subject is stored on the echo
// request context for downstream handlers, set on every allowed request
// whether the verdict came fresh from the plugin or from the cache.
const (
	ContextKeySubject     = "auth.subject"
	ContextKeySubjectName = "auth.subjectName"
)

// skipPaths are endpoints that don't require authentication.
var skipPaths = []string{
	"/api/v1/health",
	"/swagger/",
	"/metrics",
}

// NewAuthMiddleware returns an Echo middleware that validates incoming requests
// using the auth plugin via the AuthPluginHandle. Results are cached to avoid
// repeated RPC calls for the same credentials.
func NewAuthMiddleware(handle *AuthPluginHandle, cache *AuthCache) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.Path
			for _, skip := range skipPaths {
				if strings.HasPrefix(path, skip) {
					return next(c)
				}
			}

			headers := map[string][]string(c.Request().Header)
			cacheKey := computeCacheKey(headers)

			if v, found := cache.Get(cacheKey); found {
				if v.Valid {
					c.Set(ContextKeySubject, v.Subject)
					c.Set(ContextKeySubjectName, v.SubjectName)
					return next(c)
				}
				return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
			}

			resp, err := handle.Validate(&pkgauth.ValidateRequest{Headers: headers})
			if err != nil {
				// Plugin not connected yet (e.g., still initializing) — this is a
				// server-side issue, not an auth failure. Return 503 so clients
				// can distinguish from a genuine 401 and retry.
				return echo.NewHTTPError(http.StatusServiceUnavailable, "auth plugin unavailable")
			}

			if resp.CacheTTL > 0 {
				cache.Set(cacheKey, Verdict{Valid: resp.Valid, Subject: resp.Subject, SubjectName: resp.SubjectName}, resp.CacheTTL)
			}

			if !resp.Valid {
				return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
			}

			c.Set(ContextKeySubject, resp.Subject)
			c.Set(ContextKeySubjectName, resp.SubjectName)

			return next(c)
		}
	}
}

// nonAuthHeaders are headers known to be volatile or unrelated to authentication.
// Excluding them prevents per-request variance (User-Agent, X-Request-Id, etc.)
// from defeating the auth cache.
var nonAuthHeaders = map[string]bool{
	"accept":           true,
	"accept-encoding":  true,
	"accept-language":  true,
	"cache-control":    true,
	"connection":       true,
	"content-length":   true,
	"content-type":     true,
	"date":             true,
	"host":             true,
	"user-agent":       true,
	"x-correlation-id": true,
	"x-request-id":     true,
}

// computeCacheKey derives a cache key from auth-relevant request headers by
// hashing their sorted key-value pairs, excluding known volatile headers.
//
// The encoding is unambiguously decodable: each header contributes its
// length-prefixed key, then a fixed-width big-endian uint64 count of how
// many values it has, then each value with its own length prefix. The count
// is what marks where one key's values end and the next key begins, so no
// two distinct header sets can serialize to the same byte stream: not by
// splitting a value's bytes differently (closed by the length prefixes),
// and not by redistributing values across a different number of keys
// (closed by the count).
func computeCacheKey(headers map[string][]string) string {
	h := sha256.New()
	keys := make([]string, 0, len(headers))
	for k := range headers {
		if !nonAuthHeaders[strings.ToLower(k)] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var buf [8]byte
	writeUint64 := func(n uint64) {
		binary.BigEndian.PutUint64(buf[:], n)
		h.Write(buf[:])
	}
	writeLengthDelimited := func(s string) {
		writeUint64(uint64(len(s)))
		h.Write([]byte(s))
	}

	for _, k := range keys {
		writeLengthDelimited(k)
		values := headers[k]
		writeUint64(uint64(len(values)))
		for _, v := range values {
			writeLengthDelimited(v)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:16])
}

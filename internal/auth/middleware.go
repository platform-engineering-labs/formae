// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
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

			if valid, found := cache.Get(cacheKey); found {
				if valid {
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
				cache.Set(cacheKey, resp.Valid, resp.CacheTTL)
			}

			if !resp.Valid {
				return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
			}

			return next(c)
		}
	}
}

// computeCacheKey derives a cache key from the request headers by hashing
// the Authorization header value. Requests with identical Authorization
// headers will share a cache entry.
func computeCacheKey(headers map[string][]string) string {
	auth := headers["Authorization"]
	if len(auth) == 0 {
		return "no-auth"
	}
	h := sha256.Sum256([]byte(auth[0]))
	return fmt.Sprintf("%x", h[:16])
}

// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// The credential-less path asks the operator for two coordinates the template
// deployment outputs: a tenant id and a client id. Only one of them has to be
// asked for.
//
// The client id is a guid Azure generates, so nothing here can know it. The
// tenant is different: ARM names it in the challenge it returns to an
// unauthenticated request for the subscription, so it can be derived from the
// subscription the operator has already given us. Deriving it halves what they
// have to carry by hand, and a value nobody types is a value nobody mistypes.
//
// This is the same trick the Azure SDKs use to discover an authority, and it
// needs no credential: the request is expected to fail, and the useful part is
// the 401's WWW-Authenticate header.

// armSubscriptionProbe is the URL whose challenge names the tenant.
const armSubscriptionProbe = "https://management.azure.com/subscriptions/%s?api-version=2022-12-01"

// tenantProbeTimeout bounds the probe. It runs before any provisioning, on a
// path whose whole promise is that it asks nothing of this machine, so it must
// fail fast and let the operator supply the value instead of hanging.
const tenantProbeTimeout = 10 * time.Second

// authorizationURIPattern pulls the authority out of a Bearer challenge.
var authorizationURIPattern = regexp.MustCompile(`authorization_uri="([^"]+)"`)

// azureGUIDPattern matches the canonical guid form Azure uses for tenant ids.
var azureGUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// tenantFromChallenge reads the tenant out of a WWW-Authenticate value, or
// returns "" when the header names none.
//
// The tenant is the first path segment of the authority, whichever login host
// it names - sovereign clouds use their own. Anything that is not a guid yields
// "": `common` and `organizations` are valid authorities but not tenants, and
// registering a placeholder would fail later and further from the cause.
func tenantFromChallenge(header string) string {
	m := authorizationURIPattern.FindStringSubmatch(header)
	if m == nil {
		return ""
	}
	u, err := url.Parse(m[1])
	if err != nil || u.Host == "" {
		return ""
	}
	// Only the first segment can be the tenant. A guid deeper in the path would
	// be some other identifier, so this does not go looking for one.
	first := strings.Split(strings.Trim(u.Path, "/"), "/")[0]
	if !azureGUIDPattern.MatchString(first) {
		return ""
	}
	return first
}

// discoverAzureTenant asks ARM which tenant owns a subscription, without a
// credential.
//
// A var so tests can replace it: the real one makes a network call whose whole
// purpose is to be rejected.
var discoverAzureTenant = func(ctx context.Context, subscriptionID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, tenantProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf(armSubscriptionProbe, url.PathEscape(subscriptionID)), nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	// A 401 is the expected answer and the one that carries the tenant. Any
	// other status means the probe told us nothing, including a 200, which
	// would mean the request was somehow authorized and no challenge was sent.
	tenant := tenantFromChallenge(resp.Header.Get("WWW-Authenticate"))
	if tenant == "" {
		return "", fmt.Errorf("ARM did not name a tenant for subscription %s (status %d)", subscriptionID, resp.StatusCode)
	}
	return tenant, nil
}

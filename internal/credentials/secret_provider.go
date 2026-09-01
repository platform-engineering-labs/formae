// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package credentials resolves a datastore credential from its authority at
// connection time, so a credential rotated out from under a long-running
// process is picked up without restarting it.
//
// It lives outside the datastore backends deliberately. The Postgres pool is
// the first consumer, but MSSQL holds the identical defect, and a resolver
// living inside one backend is a resolver the next backend copies.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/smithy-go"
	"golang.org/x/sync/singleflight"
)

// SecretsAPI is the slice of Secrets Manager this package uses. Narrow on
// purpose: it is the seam tests drive, and widening it widens what a fake has
// to be faithful about.
type SecretsAPI interface {
	GetSecretValue(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// Clock reads the current time. Injected so a TTL is asserted rather than
// waited for.
type Clock func() time.Time

const (
	defaultTTL      = 30 * time.Second
	defaultMaxStale = 5 * time.Minute
	defaultTimeout  = 5 * time.Second
)

// SecretProvider resolves one secret, caching the value for a bounded time.
//
// The cache is what keeps the control plane off the path of every connection
// attempt. `BeforeConnect` fires per *attempt*, not per established
// connection, so an uncached resolver is consulted most heavily exactly when
// connections are failing — which is when a control plane is least able to
// help.
type SecretProvider struct {
	arnStr string
	region string
	api    SecretsAPI

	ttl      time.Duration
	maxStale time.Duration
	timeout  time.Duration
	now      Clock

	group singleflight.Group

	mu        sync.Mutex
	cached    string
	hasValue  bool
	fetchedAt time.Time
}

// Option configures a SecretProvider.
type Option func(*SecretProvider)

// WithAPI supplies the Secrets Manager client, in place of one built from the
// ambient AWS configuration.
func WithAPI(api SecretsAPI) Option { return func(p *SecretProvider) { p.api = api } }

// WithClock replaces the time source.
func WithClock(c Clock) Option { return func(p *SecretProvider) { p.now = c } }

// WithTTL sets how long a successfully read value is served before the
// authority is consulted again. It is the upper bound on how long a rotation
// goes unnoticed.
func WithTTL(d time.Duration) Option { return func(p *SecretProvider) { p.ttl = d } }

// WithMaxStale bounds how long a value may be served after refreshes start
// failing. Past it the honest answer is an error: a credential that old is as
// likely to be wrong as right.
func WithMaxStale(d time.Duration) Option { return func(p *SecretProvider) { p.maxStale = d } }

// WithTimeout bounds one lookup of the authority.
func WithTimeout(d time.Duration) Option { return func(p *SecretProvider) { p.timeout = d } }

// NewSecretProvider validates the ARN and prepares a provider for it.
//
// Validation happens here rather than at lookup time so a typo is a startup
// failure rather than an error on every connection for the life of the
// process. ctx bounds the ambient AWS configuration load, which can itself
// reach out for credentials or instance metadata: bounding only the secret
// lookup would leave startup unbounded.
func NewSecretProvider(ctx context.Context, secretARN string, opts ...Option) (*SecretProvider, error) {
	parsed, err := parseSecretARN(secretARN)
	if err != nil {
		return nil, err
	}

	p := &SecretProvider{
		arnStr:   secretARN,
		region:   parsed.Region,
		ttl:      defaultTTL,
		maxStale: defaultMaxStale,
		timeout:  defaultTimeout,
		now:      time.Now,
	}
	for _, o := range opts {
		o(p)
	}

	if p.api == nil {
		loadCtx, cancel := context.WithTimeout(ctx, p.timeout)
		defer cancel()
		cfg, err := awsconfig.LoadDefaultConfig(loadCtx, awsconfig.WithRegion(p.region))
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS configuration for %s: %w", p.region, err)
		}
		p.api = secretsmanager.NewFromConfig(cfg)
	}

	return p, nil
}

// Region is the region the provider addresses, taken from the ARN.
func (p *SecretProvider) Region() string { return p.region }

// parseSecretARN accepts only an ARN this provider could actually use.
//
// `arn.Parse` alone is not enough: it accepts plenty of well-formed ARNs that
// name something other than a readable secret, and each of those would
// otherwise surface as a per-connection runtime error.
func parseSecretARN(s string) (arn.ARN, error) {
	if s == "" {
		return arn.ARN{}, errors.New("secret ARN is empty")
	}
	if strings.TrimSpace(s) != s {
		return arn.ARN{}, fmt.Errorf("secret ARN %q has surrounding whitespace", s)
	}
	parsed, err := arn.Parse(s)
	if err != nil {
		return arn.ARN{}, fmt.Errorf("secret ARN %q is malformed: %w", s, err)
	}
	if parsed.Service != "secretsmanager" {
		return arn.ARN{}, fmt.Errorf("secret ARN %q names service %q, not secretsmanager", s, parsed.Service)
	}
	if parsed.Region == "" {
		return arn.ARN{}, fmt.Errorf("secret ARN %q names no region, so there is nothing to address", s)
	}
	if !strings.HasPrefix(parsed.Resource, "secret:") || parsed.Resource == "secret:" {
		return arn.ARN{}, fmt.Errorf("secret ARN %q does not name a secret", s)
	}
	return parsed, nil
}

// Provide returns the current credential, satisfying pkgmodel.PasswordProvider.
//
// Three lifetimes are kept apart here, and conflating any two of them breaks
// something: the shared fetch runs on its own bounded context so one caller
// giving up cannot abort a read the others are waiting on; each caller waits
// only as long as its own context allows, because pool acquisition frequently
// passes a context with no deadline; and the cache means most callers wait for
// neither.
func (p *SecretProvider) Provide(ctx context.Context) (string, error) {
	if v, ok := p.fresh(); ok {
		return v, nil
	}

	ch := p.group.DoChan(p.arnStr, func() (any, error) {
		// Re-check inside the shared call. The miss above and arriving here are
		// not atomic, so a caller can miss the cache, be descheduled, and reach
		// this point after another caller's fetch has already completed and
		// populated it. Without this, singleflight sees no call in flight and
		// starts a redundant read of the authority — which is precisely the
		// per-connection fan-out the cache exists to prevent.
		if v, ok := p.fresh(); ok {
			return v, nil
		}

		fetchCtx, cancel := context.WithTimeout(context.Background(), p.timeout)
		defer cancel()

		v, err := p.fetch(fetchCtx)
		if err == nil {
			return v, nil
		}
		// Decided inside the shared call so every waiter gets the same answer
		// rather than racing each other to a different one.
		if stale, ok := p.staleFallback(err); ok {
			return stale, nil
		}
		return "", err
	})

	select {
	case res := <-ch:
		if res.Err != nil {
			return "", res.Err
		}
		return res.Val.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// fresh reports the cached value while it is still inside the TTL.
func (p *SecretProvider) fresh() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.hasValue {
		return "", false
	}
	if p.now().Sub(p.fetchedAt) >= p.ttl {
		return "", false
	}
	return p.cached, true
}

// fetch reads the authority and, on success, replaces the cached value.
//
// Freshness is stamped when the read succeeds, not when it started: a slow
// read would otherwise be born already part-expired, and a failed one must not
// make the previous value look younger than it is.
func (p *SecretProvider) fetch(ctx context.Context) (string, error) {
	out, err := p.api.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &p.arnStr})
	if err != nil {
		return "", fmt.Errorf("failed to read secret %s: %w", p.arnStr, err)
	}
	if out.SecretString == nil {
		if len(out.SecretBinary) > 0 {
			return "", fmt.Errorf("secret %s holds a binary value; a datastore password must be a string", p.arnStr)
		}
		return "", fmt.Errorf("secret %s holds no string value", p.arnStr)
	}
	if *out.SecretString == "" {
		return "", fmt.Errorf("secret %s is empty; refusing to authenticate with an empty password", p.arnStr)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.cached = *out.SecretString
	p.hasValue = true
	p.fetchedAt = p.now()
	return p.cached, nil
}

// staleFallback serves the last good value through a transient failure, so a
// control-plane blip degrades rotation-following rather than taking the
// datastore down with it. It refuses for a permanent failure, and refuses once
// the value is older than maxStale.
func (p *SecretProvider) staleFallback(err error) (string, bool) {
	if !Transient(err) {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.hasValue {
		return "", false
	}
	if p.now().Sub(p.fetchedAt) > p.maxStale {
		return "", false
	}
	return p.cached, true
}

// Transient reports whether a failure is worth riding out on a cached value.
//
// One predicate, shared with the startup retry, because two of these would
// eventually disagree — the provider withholding a stale value while startup
// keeps retrying, or the reverse.
//
// The listed errors are the ones that will not fix themselves: a missing
// secret, a malformed request, a denial, or a key the caller cannot use.
// Anything else is treated as transient, which is the more available default
// and is safe because staleness is bounded by maxStale regardless.
func Transient(err error) bool {
	if err == nil {
		return false
	}

	var notFound *smtypes.ResourceNotFoundException
	var invalidParam *smtypes.InvalidParameterException
	var invalidReq *smtypes.InvalidRequestException
	var decryptFail *smtypes.DecryptionFailure
	if errors.As(err, &notFound) ||
		errors.As(err, &invalidParam) ||
		errors.As(err, &invalidReq) ||
		errors.As(err, &decryptFail) {
		return false
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "AccessDeniedException", "AccessDenied", "UnrecognizedClientException",
			"InvalidSignatureException", "ExpiredTokenException", "ValidationException":
			return false
		}
	}

	return true
}

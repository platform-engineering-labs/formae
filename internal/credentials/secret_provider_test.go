// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package credentials_test

// The provider resolves a datastore credential from Secrets Manager on behalf
// of every new database connection. Three properties make it more than a call:
// concurrent connections must not each fetch, a rotation must be picked up
// within a bounded lag, and a control-plane blip must not take the datastore
// down with it.
//
// Every test here drives an injected fake and an injected clock. Nothing sleeps
// and nothing reaches AWS: a TTL test that slept would be slow and flaky, and a
// clock seam is what lets "after the TTL" be an assertion rather than a wait.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/platform-engineering-labs/formae/internal/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testARN = "arn:aws:secretsmanager:us-west-2:123456789012:secret:app/db-password-AbCdEf"

// fakeSecrets is a controllable Secrets Manager. Handlers may block, so a test
// can hold a fetch in flight and prove that concurrent callers coalesce onto it
// rather than each starting their own.
type fakeSecrets struct {
	mu     sync.Mutex
	calls  atomic.Int64
	handle func(ctx context.Context, n int64) (*secretsmanager.GetSecretValueOutput, error)
}

func (f *fakeSecrets) GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	n := f.calls.Add(1)
	f.mu.Lock()
	h := f.handle
	f.mu.Unlock()
	return h(ctx, n)
}

func (f *fakeSecrets) setHandler(h func(ctx context.Context, n int64) (*secretsmanager.GetSecretValueOutput, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handle = h
}

func stringSecret(v string) *secretsmanager.GetSecretValueOutput {
	return &secretsmanager.GetSecretValueOutput{SecretString: &v}
}

func alwaysReturns(v string) *fakeSecrets {
	f := &fakeSecrets{}
	f.setHandler(func(context.Context, int64) (*secretsmanager.GetSecretValueOutput, error) {
		return stringSecret(v), nil
	})
	return f
}

// testClock is advanced explicitly by the test; it never follows wall time.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *testClock { return &testClock{t: time.Unix(1_700_000_000, 0).UTC()} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newProvider(t *testing.T, api credentials.SecretsAPI, clk *testClock, opts ...credentials.Option) *credentials.SecretProvider {
	t.Helper()
	all := append([]credentials.Option{
		credentials.WithAPI(api),
		credentials.WithClock(clk.Now),
		credentials.WithTTL(30 * time.Second),
		credentials.WithMaxStale(5 * time.Minute),
	}, opts...)
	p, err := credentials.NewSecretProvider(context.Background(), testARN, all...)
	require.NoError(t, err)
	return p
}

func TestSecretProvider_ReturnsTheSecretString(t *testing.T) {
	p := newProvider(t, alwaysReturns("hunter2"), newClock())

	got, err := p.Provide(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "hunter2", got)
}

// A binary secret is not a password with an odd encoding; handing its bytes to
// pgx would present as an authentication failure with a misleading cause.
func TestSecretProvider_RejectsBinarySecret(t *testing.T) {
	f := &fakeSecrets{}
	f.setHandler(func(context.Context, int64) (*secretsmanager.GetSecretValueOutput, error) {
		return &secretsmanager.GetSecretValueOutput{SecretBinary: []byte("hunter2")}, nil
	})
	p := newProvider(t, f, newClock())

	_, err := p.Provide(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binary")
}

func TestSecretProvider_RejectsEmptySecret(t *testing.T) {
	p := newProvider(t, alwaysReturns(""), newClock())

	_, err := p.Provide(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestSecretProvider_ConstructionRejectsUnusableARNs(t *testing.T) {
	cases := map[string]string{
		"not an arn at all":     "app/db-password",
		"malformed":             "arn:aws:secretsmanager",
		"wrong service":         "arn:aws:s3:us-west-2:123456789012:secret:app/db",
		"missing region":        "arn:aws:secretsmanager::123456789012:secret:app/db",
		"empty resource":        "arn:aws:secretsmanager:us-west-2:123456789012:",
		"not a secret resource": "arn:aws:secretsmanager:us-west-2:123456789012:key/abc",
		"surrounding space":     "  " + testARN + "  ",
		"empty":                 "",
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := credentials.NewSecretProvider(context.Background(), bad,
				credentials.WithAPI(alwaysReturns("x")))
			require.Error(t, err, "an unusable ARN must be refused at construction, not per connection")
		})
	}
}

// The region is taken from the ARN rather than configured beside it, so a
// mismatch is impossible. The assertion is on the client's configured region,
// not on a helper's return value, because only the former is what a request
// would actually use.
func TestSecretProvider_ConfiguresTheClientForTheARNRegion(t *testing.T) {
	for _, a := range []struct{ name, arn, want string }{
		{"commercial", testARN, "us-west-2"},
		{"govcloud", "arn:aws:secretsmanager:us-gov-west-1:123456789012:secret:app/db-Ab", "us-gov-west-1"},
		{"china", "arn:aws-cn:secretsmanager:cn-north-1:123456789012:secret:app/db-Ab", "cn-north-1"},
	} {
		t.Run(a.name, func(t *testing.T) {
			p, err := credentials.NewSecretProvider(context.Background(), a.arn,
				credentials.WithAPI(alwaysReturns("x")))
			require.NoError(t, err)
			assert.Equal(t, a.want, p.Region())
		})
	}
}

// The property that bounds a connection storm: many connections opening at once
// must produce one read, not one each. The fetch is held in flight and every
// caller is proven to have arrived while it was outstanding, so a warm cache
// cannot make this pass without exercising the coalescing.
func TestSecretProvider_ConcurrentLookupsCollapseToOneRead(t *testing.T) {
	const callers = 16
	release := make(chan struct{})
	arrived := make(chan struct{}, callers)

	f := &fakeSecrets{}
	f.setHandler(func(ctx context.Context, _ int64) (*secretsmanager.GetSecretValueOutput, error) {
		select {
		case <-release:
			return stringSecret("hunter2"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	p := newProvider(t, f, newClock())

	var wg sync.WaitGroup
	results := make([]string, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			arrived <- struct{}{}
			results[i], errs[i] = p.Provide(context.Background())
		}()
	}
	for range callers {
		<-arrived
	}
	// Every caller has started; the single fetch is still blocked.
	close(release)
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, "hunter2", results[i], "every waiter sees the same resolved value")
	}
	assert.Equal(t, int64(1), f.calls.Load(),
		"concurrent lookups must collapse onto one read of the authority")
}

func TestSecretProvider_ServesCachedValueWithinTheTTL(t *testing.T) {
	f := alwaysReturns("hunter2")
	clk := newClock()
	p := newProvider(t, f, clk)

	_, err := p.Provide(context.Background())
	require.NoError(t, err)
	clk.advance(29 * time.Second)
	_, err = p.Provide(context.Background())
	require.NoError(t, err)

	assert.Equal(t, int64(1), f.calls.Load(), "a lookup inside the TTL must not reach the authority")
}

// The whole point of the TTL: a rotation is followed, within a bounded lag.
func TestSecretProvider_RefetchesAfterTheTTLAndSeesTheChange(t *testing.T) {
	f := &fakeSecrets{}
	f.setHandler(func(_ context.Context, n int64) (*secretsmanager.GetSecretValueOutput, error) {
		if n == 1 {
			return stringSecret("old"), nil
		}
		return stringSecret("rotated"), nil
	})
	clk := newClock()
	p := newProvider(t, f, clk)

	first, err := p.Provide(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "old", first)

	clk.advance(31 * time.Second)
	second, err := p.Provide(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "rotated", second)
}

// Freshness is recorded when a fetch succeeds, so a failed refresh must not
// make the cached value look younger than it is and defer the next attempt.
func TestSecretProvider_FailedRefreshDoesNotAdvanceFreshness(t *testing.T) {
	f := &fakeSecrets{}
	f.setHandler(func(_ context.Context, n int64) (*secretsmanager.GetSecretValueOutput, error) {
		if n == 1 {
			return stringSecret("old"), nil
		}
		return nil, &smtypes.InternalServiceError{}
	})
	clk := newClock()
	p := newProvider(t, f, clk)

	_, err := p.Provide(context.Background())
	require.NoError(t, err)

	clk.advance(31 * time.Second)
	v, err := p.Provide(context.Background()) // fails, serves stale
	require.NoError(t, err)
	assert.Equal(t, "old", v)

	// Still expired, so this must attempt again rather than treat the failed
	// refresh as having refreshed anything.
	_, err = p.Provide(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), f.calls.Load(),
		"a failed refresh must leave the entry expired")
}

func TestSecretProvider_TransientErrorServesLastGoodValue(t *testing.T) {
	f := &fakeSecrets{}
	f.setHandler(func(_ context.Context, n int64) (*secretsmanager.GetSecretValueOutput, error) {
		if n == 1 {
			return stringSecret("good"), nil
		}
		return nil, &smtypes.InternalServiceError{}
	})
	clk := newClock()
	p := newProvider(t, f, clk)

	_, err := p.Provide(context.Background())
	require.NoError(t, err)
	clk.advance(31 * time.Second)

	v, err := p.Provide(context.Background())
	require.NoError(t, err, "a control-plane blip must not take the datastore down with it")
	assert.Equal(t, "good", v)
}

// Access denied is not something to ride out, and serving a credential the
// caller may no longer be entitled to read is worse than failing.
func TestSecretProvider_PermanentErrorWithholdsStale(t *testing.T) {
	f := &fakeSecrets{}
	f.setHandler(func(_ context.Context, n int64) (*secretsmanager.GetSecretValueOutput, error) {
		if n == 1 {
			return stringSecret("good"), nil
		}
		return nil, &smtypes.ResourceNotFoundException{}
	})
	clk := newClock()
	p := newProvider(t, f, clk)

	_, err := p.Provide(context.Background())
	require.NoError(t, err)
	clk.advance(31 * time.Second)

	_, err = p.Provide(context.Background())
	require.Error(t, err, "a permanent failure must not be papered over with a stale value")
}

// Stale is bounded. Beyond the limit the honest answer is an error, because a
// credential that old is as likely to be wrong as right.
func TestSecretProvider_StaleIsNotServedBeyondTheMaximum(t *testing.T) {
	f := &fakeSecrets{}
	f.setHandler(func(_ context.Context, n int64) (*secretsmanager.GetSecretValueOutput, error) {
		if n == 1 {
			return stringSecret("good"), nil
		}
		return nil, &smtypes.InternalServiceError{}
	})
	clk := newClock()
	p := newProvider(t, f, clk)

	_, err := p.Provide(context.Background())
	require.NoError(t, err)

	clk.advance(6 * time.Minute) // past MaxStale
	_, err = p.Provide(context.Background())
	require.Error(t, err)
}

func TestSecretProvider_TransientErrorWithNoLastGoodValueReturnsError(t *testing.T) {
	f := &fakeSecrets{}
	f.setHandler(func(context.Context, int64) (*secretsmanager.GetSecretValueOutput, error) {
		return nil, &smtypes.InternalServiceError{}
	})
	p := newProvider(t, f, newClock())

	_, err := p.Provide(context.Background())
	require.Error(t, err, "there is nothing to fall back to on the first fetch")
}

// A caller giving up must not abort a fetch the other waiters are relying on.
func TestSecretProvider_CancelledWaiterDoesNotCancelTheSharedFetch(t *testing.T) {
	release := make(chan struct{})
	f := &fakeSecrets{}
	f.setHandler(func(ctx context.Context, _ int64) (*secretsmanager.GetSecretValueOutput, error) {
		select {
		case <-release:
			return stringSecret("hunter2"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	p := newProvider(t, f, newClock())

	started := make(chan struct{})
	stayed := make(chan error, 1)
	go func() {
		close(started)
		_, err := p.Provide(context.Background())
		stayed <- err
	}()
	<-started

	// A second caller arrives and gives up while the shared fetch is in flight.
	giveUp, cancel := context.WithCancel(context.Background())
	leaving := make(chan error, 1)
	go func() {
		_, err := p.Provide(giveUp)
		leaving <- err
	}()
	cancel()
	require.Error(t, <-leaving, "the cancelled caller stops waiting")

	close(release)
	require.NoError(t, <-stayed, "the remaining caller still receives the shared result")
}

// The caller's context frequently has no deadline at all — pool acquisition is
// the ordinary case — so the provider must bound the lookup itself or a hung
// control plane occupies connection-creation capacity indefinitely.
func TestSecretProvider_LookupTimesOutIndependentlyOfACallerWithNoDeadline(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)

	f := &fakeSecrets{}
	f.setHandler(func(ctx context.Context, _ int64) (*secretsmanager.GetSecretValueOutput, error) {
		select {
		case <-blocked:
			return stringSecret("never"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	p := newProvider(t, f, newClock(), credentials.WithTimeout(50*time.Millisecond))

	_, err := p.Provide(context.Background())
	require.Error(t, err, "a hung lookup must fail rather than hang a caller with no deadline")
	assert.True(t, errors.Is(err, context.DeadlineExceeded),
		"the failure should be the provider's own deadline, got: %v", err)
}

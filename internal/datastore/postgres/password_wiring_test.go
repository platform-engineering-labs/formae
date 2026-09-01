// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres_test

// These cover the step that turns a dormant seam into a live one: config naming
// a secret producing a datastore that resolves its credential per connection.
//
// It is the part where a mistake is silent. A datastore that quietly fell back
// to the static password would pass every functional test in this package while
// being exactly the failure mode the design forbids — an agent that looks
// healthy and dies one rotation later. So the controls matter more than the
// happy paths here.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/platform-engineering-labs/formae/internal/datastore/postgres"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fakeSecretARN = "arn:aws:secretsmanager:us-west-2:123456789012:secret:app/db-AbCdEf"

// wiringConfig builds a datastore config pointed at a rotatable role, with the
// provider supplied through the factory seam so the authority is controllable
// without reaching AWS.
func wiringConfig(role, database, password, secretARN string, factory func(context.Context, string) (pkgmodel.PasswordProvider, error)) *pkgmodel.DatastoreConfig {
	return &pkgmodel.DatastoreConfig{
		DatastoreType: "postgres",
		Postgres: pkgmodel.PostgresConfig{
			Host:                    "localhost",
			Port:                    5432,
			User:                    role,
			Password:                password,
			Database:                database,
			PasswordSecretArn:       secretARN,
			PasswordProviderFactory: factory,
		},
	}
}

func TestNewDatastorePostgres_ArnConfiguresAProvider(t *testing.T) {
	role, database, password := rotatableRole(t)

	var asked atomic.Int64
	cfg := wiringConfig(role, database, password, fakeSecretARN,
		func(_ context.Context, arn string) (pkgmodel.PasswordProvider, error) {
			assert.Equal(t, fakeSecretARN, arn, "the factory receives the configured ARN")
			return func(context.Context) (string, error) {
				asked.Add(1)
				return password, nil
			}, nil
		})

	_, err := postgres.NewDatastorePostgres(context.Background(), cfg, "agent-1")
	require.NoError(t, err)
	assert.Positive(t, asked.Load(), "the configured secret must actually be resolved")
}

// The regression guard for every deployment that never opts in. The factory
// standing in for AWS must not be reached at all: eagerly building a client
// when no ARN is configured would add startup latency and a credential lookup
// nobody asked for, while every functional test still passed.
func TestNewDatastorePostgres_NoArnLeavesTheStaticPath(t *testing.T) {
	role, database, password := rotatableRole(t)

	var built atomic.Bool
	cfg := wiringConfig(role, database, password, "",
		func(context.Context, string) (pkgmodel.PasswordProvider, error) {
			built.Store(true)
			return nil, errors.New("must not be called")
		})

	d, err := postgres.NewDatastorePostgres(context.Background(), cfg, "agent-1")
	require.NoError(t, err)
	assert.False(t, built.Load(), "no ARN must mean no provider is built at all")
	assert.Nil(t, cfg.Postgres.PasswordProvider, "the static path must stay static")
	require.NoError(t, d.(postgres.DatastorePostgres).Pool().Ping(context.Background()))
}

func TestNewDatastorePostgres_UnreadableSecretFailsConstruction(t *testing.T) {
	role, database, password := rotatableRole(t)

	cfg := wiringConfig(role, database, password, fakeSecretARN,
		func(context.Context, string) (pkgmodel.PasswordProvider, error) {
			return func(context.Context) (string, error) {
				return "", &smtypes.ResourceNotFoundException{}
			}, nil
		})

	_, err := postgres.NewDatastorePostgres(context.Background(), cfg, "agent-1")
	require.Error(t, err, "an unreadable secret must fail startup, not the first connection")
}

// The control that matters most. The static password here is correct, so a
// datastore that fell back to it would construct successfully and look fine.
func TestNewDatastorePostgres_UnreadableSecretDoesNotFallBackToPassword(t *testing.T) {
	role, database, password := rotatableRole(t)

	cfg := wiringConfig(role, database, password, fakeSecretARN,
		func(context.Context, string) (pkgmodel.PasswordProvider, error) {
			return func(context.Context) (string, error) {
				return "", &smtypes.ResourceNotFoundException{}
			}, nil
		})

	_, err := postgres.NewDatastorePostgres(context.Background(), cfg, "agent-1")
	require.Error(t, err,
		"falling back to the working static password would hide the broken secret until a rotation")
}

// Proves the readiness check exists, rather than that a wrong credential fails
// somewhere.
//
// A credential that is simply wrong fails at the migration connection, which is
// opened before the pool exists — so asserting only "construction fails" passes
// whether or not the readiness check is there at all. That was the first
// version of this test, and removing the check did not fail it.
//
// This isolates the check by making the credential correct when migrations
// resolve it and wrong for every connection the pool opens afterwards, which is
// also the real divergence the constructor allows: migrations resolve once into
// their own connection string, while the pool resolves per connection, so a
// rotation landing between them is expressible.
func TestNewDatastorePostgres_CredentialThatBreaksAfterMigrationsFailsReadiness(t *testing.T) {
	role, database, password := rotatableRole(t)

	var calls atomic.Int64
	cfg := wiringConfig(role, database, password, fakeSecretARN,
		func(context.Context, string) (pkgmodel.PasswordProvider, error) {
			return func(context.Context) (string, error) {
				// The warm fetch and the migration DSN take the working value;
				// everything the pool opens afterwards gets a rejected one.
				if calls.Add(1) <= 2 {
					return password, nil
				}
				return "definitely_not_" + password, nil
			}, nil
		})

	_, err := postgres.NewDatastorePostgres(context.Background(), cfg, "agent-1")
	require.Error(t, err,
		"the pool's own credential must be proven to authenticate before construction returns")
	assert.Contains(t, err.Error(), "credential",
		"the failure should be attributed to the credential, not to a generic startup error")
}

// A control-plane blip during a rolling deployment must not stop a task
// becoming healthy.
func TestNewDatastorePostgres_TransientSecretErrorIsRetriedAtStartup(t *testing.T) {
	role, database, password := rotatableRole(t)

	var attempts atomic.Int64
	cfg := wiringConfig(role, database, password, fakeSecretARN,
		func(context.Context, string) (pkgmodel.PasswordProvider, error) {
			return func(context.Context) (string, error) {
				if attempts.Add(1) <= 2 {
					return "", &smtypes.InternalServiceError{}
				}
				return password, nil
			}, nil
		})

	_, err := postgres.NewDatastorePostgres(context.Background(), cfg, "agent-1")
	require.NoError(t, err, "a transient failure must be waited out, not fatal")
	assert.GreaterOrEqual(t, attempts.Load(), int64(3), "the first two attempts should have been retried")
}

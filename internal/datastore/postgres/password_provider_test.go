// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/jackc/pgx/v5"
	"github.com/platform-engineering-labs/formae/internal/datastore/postgres"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adminConnect opens a connection as the container's superuser, skipping the
// test when Postgres is not reachable.
func adminConnect(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), "postgres://postgres:admin@localhost:5432/postgres")
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	return conn
}

// rotatableRole creates a login role with its own database, so a test can change
// that role's password without affecting any other user of the Postgres server.
// It returns the role name, the database name and the initial password.
func rotatableRole(t *testing.T) (role, database, password string) {
	t.Helper()
	admin := adminConnect(t)

	suffix := mksuid.New().String()
	role = "rot_user_" + suffix
	database = "rot_db_" + suffix
	password = "initial_" + suffix

	_, err := admin.Exec(context.Background(),
		fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD %s",
			pgx.Identifier{role}.Sanitize(), quoteLiteral(password)))
	require.NoError(t, err)

	_, err = admin.Exec(context.Background(),
		fmt.Sprintf("CREATE DATABASE %s OWNER %s",
			pgx.Identifier{database}.Sanitize(), pgx.Identifier{role}.Sanitize()))
	require.NoError(t, err)

	require.NoError(t, admin.Close(context.Background()))

	t.Cleanup(func() {
		cleanup := adminConnect(t)
		defer func() { _ = cleanup.Close(context.Background()) }()
		_, _ = cleanup.Exec(context.Background(),
			fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", pgx.Identifier{database}.Sanitize()))
		_, _ = cleanup.Exec(context.Background(),
			fmt.Sprintf("DROP ROLE IF EXISTS %s", pgx.Identifier{role}.Sanitize()))
	})

	return role, database, password
}

// quoteLiteral renders a Postgres single-quoted string literal.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// setRolePassword changes a role's password using a superuser connection.
func setRolePassword(t *testing.T, role, password string) {
	t.Helper()
	admin := adminConnect(t)
	defer func() { _ = admin.Close(context.Background()) }()
	_, err := admin.Exec(context.Background(),
		fmt.Sprintf("ALTER ROLE %s PASSWORD %s",
			pgx.Identifier{role}.Sanitize(), quoteLiteral(password)))
	require.NoError(t, err)
}

// credential is a concurrency-safe answer for a PasswordProvider: it hands out
// either a password or an error, records how often it was asked, and can be
// changed while a pool is live.
type credential struct {
	mu       sync.Mutex
	password string
	err      error
	calls    int
}

func (c *credential) provide(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.err != nil {
		return "", c.err
	}
	return c.password, nil
}

func (c *credential) set(password string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.password = password
	c.err = err
}

func (c *credential) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// newRotatableDatastore builds a Postgres datastore against a dedicated role and
// database, optionally driven by a password provider.
func newRotatableDatastore(t *testing.T, role, database, password string, provider pkgmodel.PasswordProvider) postgres.DatastorePostgres {
	t.Helper()
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.PostgresDatastore,
		Postgres: pkgmodel.PostgresConfig{
			Host:             "localhost",
			Port:             5432,
			User:             role,
			Password:         password,
			PasswordProvider: provider,
			Database:         database,
		},
	}
	iface, err := postgres.NewDatastorePostgres(context.Background(), cfg, "test")
	require.NoError(t, err)
	d, ok := iface.(postgres.DatastorePostgres)
	require.True(t, ok)
	t.Cleanup(d.Close)
	return d
}

// ping runs a trivial query on a freshly acquired pool connection.
func ping(ctx context.Context, d postgres.DatastorePostgres) error {
	conn, err := d.Pool().Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var one int
	return conn.QueryRow(ctx, "SELECT 1").Scan(&one)
}

func TestPostgresDatastore_WithoutProviderUsesStaticPassword(t *testing.T) {
	role, database, password := rotatableRole(t)
	d := newRotatableDatastore(t, role, database, password, nil)

	require.NoError(t, ping(context.Background(), d))

	// A reset discards every pooled connection, so the next acquire has to open
	// a fresh one and still authenticate with the configured password.
	d.Pool().Reset()
	require.NoError(t, ping(context.Background(), d))
}

func TestPostgresDatastore_PasswordProviderIsCalledPerNewConnection(t *testing.T) {
	role, database, password := rotatableRole(t)
	cred := &credential{password: password}
	d := newRotatableDatastore(t, role, database, password, cred.provide)

	before := cred.callCount()

	first, err := d.Pool().Acquire(context.Background())
	require.NoError(t, err)
	second, err := d.Pool().Acquire(context.Background())
	require.NoError(t, err)
	first.Release()
	second.Release()

	assert.Equal(t, before+2, cred.callCount(),
		"provider should be consulted once for each connection the pool opens")
}

func TestPostgresDatastore_RotatedPasswordIsUsedForNewConnections(t *testing.T) {
	role, database, password := rotatableRole(t)
	cred := &credential{password: password}
	d := newRotatableDatastore(t, role, database, password, cred.provide)

	require.NoError(t, ping(context.Background(), d))

	rotated := "rotated_" + mksuid.New().String()
	setRolePassword(t, role, rotated)

	// The rotation really did invalidate the old credential.
	_, err := pgx.Connect(context.Background(),
		postgres.BuildConnStr("localhost", 5432, role, password, database))
	require.Error(t, err)

	cred.set(rotated, nil)

	d.Pool().Reset()
	require.NoError(t, ping(context.Background(), d),
		"a new connection should authenticate with the password the provider returns now")
}

func TestPostgresDatastore_PasswordProviderErrorFailsTheConnection(t *testing.T) {
	role, database, password := rotatableRole(t)
	cred := &credential{password: password}
	d := newRotatableDatastore(t, role, database, password, cred.provide)

	require.NoError(t, ping(context.Background(), d))

	sentinel := errors.New("secret store unavailable")
	cred.set("", sentinel)

	d.Pool().Reset()
	err := ping(context.Background(), d)
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

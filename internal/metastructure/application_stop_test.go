// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// freePort reserves an ephemeral TCP port and returns it for the node to bind.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

// startedMetastructure builds and starts a metastructure on an in-memory
// datastore with its own ergo ports, so tests neither collide with each other
// nor with an agent running on the machine.
func startedMetastructure(t *testing.T) *Metastructure {
	t.Helper()
	cfg := &pkgmodel.Config{
		Agent: pkgmodel.AgentConfig{
			Server: pkgmodel.ServerConfig{
				Nodename:      fmt.Sprintf("app-stop-test-%d", time.Now().UnixNano()),
				Hostname:      "localhost",
				Secret:        "test-secret",
				ErgoPort:      freePort(t),
				RegistrarPort: freePort(t),
			},
			Datastore: pkgmodel.DatastoreConfig{
				DatastoreType: pkgmodel.SqliteDatastore,
				Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
			},
		},
	}
	m, err := NewMetastructure(context.Background(), cfg, nil, nil, "test")
	require.NoError(t, err)
	return m
}

// A collapsed supervision tree must be reported: once the orchestrator
// application stops, the actors that execute commands, persist state, and run
// rotations are gone, while the process's HTTP surface keeps serving. The
// process has to learn about the stop so it can exit and be restarted by its
// supervisor, which is the path that re-runs incomplete commands.
func TestMetastructure_ReportsAbnormalApplicationStop(t *testing.T) {
	m := startedMetastructure(t)
	stopped := make(chan error, 1)
	m.OnApplicationStopped = func(reason error) { stopped <- reason }

	require.NoError(t, m.Start())
	t.Cleanup(func() { m.Stop(false) })

	// Repeated abnormal exits of one supervised child inside the supervisor's
	// restart period exhaust its restart intensity and collapse the tree.
	deadline := time.After(15 * time.Second)
	for {
		select {
		case reason := <-stopped:
			require.Error(t, reason, "the callback must carry the stop reason")
			require.NotErrorIs(t, reason, gen.TerminateReasonNormal)
			require.NotErrorIs(t, reason, gen.TerminateReasonShutdown)
			return
		case <-deadline:
			t.Fatal("the application stopping abnormally was never reported")
		default:
		}
		if pid, err := m.Node.ProcessPID(gen.Atom("FormaCommandPersister")); err == nil {
			_ = m.Node.SendExit(pid, errors.New("injected failure"))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The same callback must stay silent on a deliberate shutdown: a process that
// treats its own graceful stop as a failure turns every deploy into an error.
func TestMetastructure_GracefulStopIsNotReported(t *testing.T) {
	m := startedMetastructure(t)
	stopped := make(chan error, 1)
	m.OnApplicationStopped = func(reason error) { stopped <- reason }

	require.NoError(t, m.Start())
	m.Stop(false)

	select {
	case reason := <-stopped:
		t.Fatalf("a graceful stop must not be reported, got: %v", reason)
	case <-time.After(2 * time.Second):
	}
}

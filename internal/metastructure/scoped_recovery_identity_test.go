//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestScopedApplyReapedRecoveryIdentityEvidence(t *testing.T) {
	for _, scenario := range []string{"matching", "wrong-target", "unknown-target", "wrong-stack", "unmanaged", "explicit"} {
		t.Run(scenario, func(t *testing.T) {
			ds := newSQLiteTestDatastore(t)
			_, err := ds.CreateStack(&pkgmodel.Stack{Label: "s"}, "seed")
			require.NoError(t, err)
			_, err = ds.CreateTarget(&pkgmodel.Target{Label: "t"})
			require.NoError(t, err)
			target, err := ds.LoadTarget("t")
			require.NoError(t, err)
			r := &pkgmodel.Resource{Ksuid: "original", Stack: "s", Target: "t", Label: "r", Type: "Test::Resource", Managed: scenario != "unmanaged", Properties: []byte(`{}`)}
			_, err = ds.StoreResource(r, "seed", target.Health.IncarnationID)
			require.NoError(t, err)
			conn := ds.(dssqlite.DatastoreSQLite).Conn()
			// Mutate physical columns to model retained historical rows, never JSON or
			// label-derived observation evidence. All reads use the real datastore.
			_, err = conn.Exec("UPDATE targets SET health_state='reaped' WHERE label='t'")
			require.NoError(t, err)
			_, err = conn.Exec("UPDATE resources SET operation='reaped' WHERE ksuid='original'")
			require.NoError(t, err)
			if scenario == "wrong-target" || scenario == "unknown-target" {
				physical := "older-incarnation"
				if scenario == "unknown-target" {
					physical = ""
				}
				_, err = conn.Exec("UPDATE resources SET target_incarnation_id=? WHERE ksuid='original'", physical)
				require.NoError(t, err)
				observation, err := ds.(datastore.ResourceObservationReader).GetResourceObservation("original")
				require.NoError(t, err)
				require.Equal(t, physical, observation.TargetIncarnationID)
			}
			if scenario == "wrong-stack" {
				_, err = ds.DeleteStack("s", "delete")
				require.NoError(t, err)
				_, err = ds.CreateStack(&pkgmodel.Stack{Label: "s"}, "recreate")
				require.NoError(t, err)
			}
			f := &pkgmodel.Forma{Targets: []pkgmodel.Target{{Label: "t"}}, Resources: []pkgmodel.Resource{{Stack: "s", Target: "t", Label: "r", Type: "Test::Resource"}}}
			if scenario == "explicit" {
				f.Resources[0].Ksuid = "explicit"
			}
			err = pinReapedRecoveryIdentities(ds, f)
			switch scenario {
			case "wrong-target", "unknown-target", "wrong-stack":
				require.ErrorContains(t, err, "incarnation")
				require.Empty(t, f.Resources[0].Ksuid)
			case "unmanaged":
				require.NoError(t, err)
				require.Empty(t, f.Resources[0].Ksuid)
			case "explicit":
				require.NoError(t, err)
				require.Equal(t, "explicit", f.Resources[0].Ksuid)
			default:
				require.NoError(t, err)
				require.Equal(t, "original", f.Resources[0].Ksuid)
			}
		})
	}
}

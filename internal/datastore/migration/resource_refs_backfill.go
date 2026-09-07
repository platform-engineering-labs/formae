// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package migration

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// resourceRefsWriter is the capability required to write the refs column.
// Only postgres and aurora implement this method; sqlite and mssql do not have
// the refs column, so the backfill is skipped for those dialects.
type resourceRefsWriter interface {
	UpdateResourceRefs(uri, version string, refs []string) error
}

// BackfillResourceRefs is a one-time, idempotent sweep that materializes each
// stored resource version's outbound reference KSUIDs into its refs column.
// Pre-migration rows default to an empty refs array; this function recomputes
// the correct value from the stored data JSON and writes it back so every
// version is queryable by the indexed cascade lookup — regardless of when it
// was written.
//
// Idempotent: recomputing refs from the stored data and setting the column
// yields the same result on every run, so repeated boots are safe.
//
// Skipped on datastores that do not implement UpdateResourceRefs (e.g. sqlite,
// mssql), which have no refs column. The check is a capability type-assert, so
// no per-boot paging cost is incurred on those dialects.
func BackfillResourceRefs(ds datastore.Datastore) error {
	writer, ok := ds.(resourceRefsWriter)
	if !ok {
		slog.Debug("datastore does not support refs column — skipping resource refs backfill")
		return nil
	}

	afterURI, afterVersion := "", ""
	for {
		page, err := ds.LoadResourceVersionsPage(afterURI, afterVersion, resourceVersionPageSize)
		if err != nil {
			return fmt.Errorf("backfill resource refs: load resource versions page: %w", err)
		}
		if len(page) == 0 {
			break
		}
		for _, v := range page {
			data, err := json.Marshal(v.Resource)
			if err != nil {
				return fmt.Errorf("backfill resource refs: marshal resource %s version %s: %w", v.URI, v.Version, err)
			}
			refs := pkgmodel.CollectReferencedKSUIDs(data)
			if err := writer.UpdateResourceRefs(v.URI, v.Version, refs); err != nil {
				return fmt.Errorf("backfill resource refs: update refs for %s version %s: %w", v.URI, v.Version, err)
			}
			afterURI, afterVersion = v.URI, v.Version
		}
		if len(page) < resourceVersionPageSize {
			break
		}
	}

	return nil
}

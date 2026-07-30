// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/metastructure/querier"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ListResourceSummaries returns a lightweight projection of resources matching
// query. It delegates directly to the datastore's summary path — no full data
// unmarshal, no KSUID rewrite, no stack/policy/target enrichment.
func (m *Metastructure) ListResourceSummaries(query string) ([]pkgmodel.ResourceSummary, error) {
	q := querier.NewBlugeQuerier(m.Datastore)
	summaries, err := q.QueryResourceSummaries(query)
	if err != nil {
		slog.Debug("Cannot get resource summaries from query", "error", err)
		return nil, err
	}
	return summaries, nil
}

// ExtractResourceByKsuid loads the single resource identified by ksuid from the
// datastore, performs the KSUID→triplet rewrite on its Properties and
// ReadOnlyProperties, and returns it. Returns nil, nil when no resource with that
// ksuid exists or when the resource's latest version is a delete or reaped tombstone.
func (m *Metastructure) ExtractResourceByKsuid(ksuid string) (*pkgmodel.Resource, error) {
	res, err := m.Datastore.LoadLatestResourceByKsuid(ksuid)
	if err != nil {
		slog.Debug("Cannot load resource by ksuid", "ksuid", ksuid, "error", err)
		return nil, err
	}
	if res == nil {
		return nil, nil
	}

	resources := []*pkgmodel.Resource{res}
	if err := m.reverseTranslateKSUIDsToTriplets(resources); err != nil {
		slog.Error("Failed to reverse translate KSUIDs to triplets", "ksuid", ksuid, "error", err)
		return nil, err
	}

	return resources[0], nil
}

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package opsmgr

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/orbital/mgr"
	"github.com/platform-engineering-labs/orbital/opm/security"
	"github.com/platform-engineering-labs/orbital/opm/tree"
	"github.com/platform-engineering-labs/orbital/ops"
	"github.com/platform-engineering-labs/orbital/platform"
)

// New constructs a Manager from a single URL + channel.
// Kept for backwards compatibility. Prefer NewFromRepositories.
func New(logger *slog.Logger, uri url.URL, channel string) (*mgr.Manager, error) {
	repos := []pkgmodel.Repository{{URI: uri, Type: pkgmodel.RepositoryTypeBinary}}
	return NewFromRepositories(logger, repos, channel)
}

// NewFromRepositories constructs a Manager with all configured repos loaded.
// Channel (if non-empty) overrides the URI fragment for every repo.
func NewFromRepositories(logger *slog.Logger, repos []pkgmodel.Repository, channel string) (*mgr.Manager, error) {
	return newManager(logger, repos, channel)
}

// NewFromRepositoriesFiltered constructs a Manager containing only repos whose
// type is in the allowed set. Empty allowed = all types.
func NewFromRepositoriesFiltered(logger *slog.Logger, repos []pkgmodel.Repository, channel string, allowed ...pkgmodel.RepositoryType) (*mgr.Manager, error) {
	if len(allowed) == 0 {
		return newManager(logger, repos, channel)
	}
	allow := make(map[pkgmodel.RepositoryType]bool, len(allowed))
	for _, a := range allowed {
		allow[a] = true
	}
	filtered := make([]pkgmodel.Repository, 0, len(repos))
	for _, r := range repos {
		if allow[r.Type] {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no matching repositories after filtering")
	}
	return newManager(logger, filtered, channel)
}

func newManager(logger *slog.Logger, repos []pkgmodel.Repository, channel string) (*mgr.Manager, error) {
	binPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("could not determine binary path: %w", err)
	}

	opsRepos := make([]ops.Repository, 0, len(repos))
	for _, r := range repos {
		uri := r.URI
		if channel != "" {
			uri.Fragment = channel
		}
		opsRepos = append(opsRepos, ops.Repository{Uri: uri, Priority: 0, Enabled: true})
	}

	if len(opsRepos) == 0 {
		return nil, fmt.Errorf("no repositories configured")
	}

	return mgr.New(logger, filepath.Dir(filepath.Dir(binPath)), &tree.Config{
		OS:           platform.Current().OS,
		Arch:         platform.Current().Arch,
		Security:     security.Default,
		Repositories: opsRepos,
	})
}

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
	"github.com/platform-engineering-labs/orbital"
	"github.com/platform-engineering-labs/orbital/mgr"
	"github.com/platform-engineering-labs/orbital/opm/security"
	"github.com/platform-engineering-labs/orbital/opm/tree"
	"github.com/platform-engineering-labs/orbital/ops"
	"github.com/platform-engineering-labs/orbital/platform"
)

// New constructs a Manager from a single URL + channel.
// Kept for backwards compatibility. Prefer NewFromRepositories.
func New(logger *slog.Logger, uri url.URL, channel string, sudo bool, writable bool) (*mgr.Manager, error) {
	repos := []pkgmodel.Repository{{URI: uri, Type: pkgmodel.RepositoryTypeBinary}}
	return NewFromRepositories(logger, repos, channel, sudo, writable)
}

// NewFromRepositories constructs a Manager with all configured repos loaded.
// Channel (if non-empty) overrides the URI fragment for every repo.
func NewFromRepositories(logger *slog.Logger, repos []pkgmodel.Repository, channel string, sudo bool, writable bool) (*mgr.Manager, error) {
	return newManager(logger, repos, channel, sudo, writable)
}

// NewFromRepositoriesFiltered constructs a Manager containing only repos whose
// type is in the allowed set. Empty allowed = all types.
func NewFromRepositoriesFiltered(logger *slog.Logger, repos []pkgmodel.Repository, channel string, sudo bool, writable bool, allowed ...pkgmodel.RepositoryType) (*mgr.Manager, error) {
	if len(allowed) == 0 {
		return newManager(logger, repos, channel, sudo, writable)
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
	return newManager(logger, filtered, channel, sudo, writable)
}

// FormaePelRootEnv overrides the orbital tree root that opsmgr would
// otherwise derive from os.Executable(). The default derivation
// (filepath.Dir(filepath.Dir(binPath))) only points at a real tree when
// the binary is installed under /opt/pel/bin/formae; a freshly built
// binary run from a worktree resolves to a path with no tree, leaving
// orbital's internal cache/pki/lock fields nil — first call panics. Set
// FORMAE_PEL_ROOT=/opt/pel (or any other initialized tree) to unblock
// dev runs and isolated tests.
const FormaePelRootEnv = "FORMAE_PEL_ROOT"

func newManager(logger *slog.Logger, repos []pkgmodel.Repository, channel string, sudo bool, writable bool) (*mgr.Manager, error) {
	treePath, err := resolveTreePath()
	if err != nil {
		return nil, err
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

	var opts []orbital.Option

	if sudo {
		opts = append(opts, orbital.WithSudo())
	}

	if writable {
		opts = append(opts, orbital.WithWritable())
	}

	opts = append(opts, orbital.WithEmbedded(
		treePath, &tree.Config{
			OS:           platform.Current().OS,
			Arch:         platform.Current().Arch,
			Security:     security.Default,
			Repositories: opsRepos,
		},
	))

	return mgr.New(logger, opts...)
}

// resolveTreePath returns FORMAE_PEL_ROOT when set, otherwise the
// directory two levels above the running binary (matches the
// /opt/pel/bin/formae → /opt/pel install layout).
func resolveTreePath() (string, error) {
	if root := os.Getenv(FormaePelRootEnv); root != "" {
		return root, nil
	}
	binPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not determine binary path: %w", err)
	}
	return filepath.Dir(filepath.Dir(binPath)), nil
}

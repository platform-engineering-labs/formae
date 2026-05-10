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
	"syscall"

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

// FormaePelRootEnv overrides the orbital tree root that opsmgr would
// otherwise derive from os.Executable(). The default derivation
// (filepath.Dir(filepath.Dir(binPath))) only points at a real tree when
// the binary is installed under /opt/pel/bin/formae; a freshly built
// binary run from a worktree resolves to a path with no tree, leaving
// orbital's internal cache/pki/lock fields nil — first call panics. Set
// FORMAE_PEL_ROOT=/opt/pel (or any other initialized tree) to unblock
// dev runs and isolated tests.
const FormaePelRootEnv = "FORMAE_PEL_ROOT"

func newManager(logger *slog.Logger, repos []pkgmodel.Repository, channel string) (*mgr.Manager, error) {
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

	return mgr.New(logger, treePath, &tree.Config{
		OS:           platform.Current().OS,
		Arch:         platform.Current().Arch,
		Security:     security.Default,
		Repositories: opsRepos,
	})
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

// TreeRequiresElevation reports whether constructing an orbital manager
// against the configured tree would re-exec the process under sudo.
// orbital's tree.New triggers a sudo re-exec whenever the tree path is
// root-owned and the caller is not root — including for read-only
// operations like List(). Callers that want to remain unprivileged
// (e.g. `plugin list`) check this first and skip the local arm when it
// would force a sudo prompt.
//
// Returns false (with no error) if the tree path can't be stat'd; the
// caller can then attempt orbital construction and rely on the existing
// Ready() guard.
func TreeRequiresElevation() bool {
	path, err := resolveTreePath()
	if err != nil {
		return false
	}
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	sys, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if sys.Uid != 0 {
		return false
	}
	return os.Geteuid() != 0
}

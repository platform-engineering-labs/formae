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

	"github.com/platform-engineering-labs/orbital/mgr"
	"github.com/platform-engineering-labs/orbital/opm/security"
	"github.com/platform-engineering-labs/orbital/opm/tree"
	"github.com/platform-engineering-labs/orbital/ops"
	"github.com/platform-engineering-labs/orbital/platform"
)

func New(logger *slog.Logger, uri url.URL, channel string) (*mgr.Manager, error) {
	binPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("could not determine binary path: %w", err)
	}

	if channel != "" {
		uri.Fragment = channel
	}

	return mgr.New(logger, filepath.Dir(filepath.Dir(binPath)), &tree.Config{
		OS:       platform.Current().OS,
		Arch:     platform.Current().Arch,
		Security: security.Default,
		Repositories: []ops.Repository{
			{
				Uri:      uri,
				Priority: 0,
				Enabled:  true,
			},
		},
	})
}

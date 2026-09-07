// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"bufio"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// listAWSProfiles enumerates the profiles the AWS shared config names,
// honoring AWS_CONFIG_FILE and AWS_SHARED_CREDENTIALS_FILE. The config file
// prefixes sections with `profile ` (except default); the credentials file
// does not. Section types we do not use (sso-session, services) are skipped:
// they are not connectable identities. The SDK ships no enumerator, which is
// why this small parser exists at all.
func listAWSProfiles() ([]string, error) {
	seen := map[string]bool{}
	add := func(path string, configConventions bool) error {
		f, err := os.Open(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
				continue
			}
			name := strings.TrimSpace(line[1 : len(line)-1])
			if configConventions {
				switch {
				case name == "default":
				case strings.HasPrefix(name, "profile "):
					name = strings.TrimSpace(strings.TrimPrefix(name, "profile "))
				default:
					continue // sso-session, services, or malformed
				}
			}
			if name != "" {
				seen[name] = true
			}
		}
		return sc.Err()
	}
	if err := add(configFilePath(), true); err != nil {
		return nil, err
	}
	if err := add(credentialsFilePath(), false); err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

func configFilePath() string {
	if p, ok := os.LookupEnv("AWS_CONFIG_FILE"); ok && p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aws", "config")
}

func credentialsFilePath() string {
	if p, ok := os.LookupEnv("AWS_SHARED_CREDENTIALS_FILE"); ok && p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aws", "credentials")
}

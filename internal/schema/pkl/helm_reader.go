// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"os/exec"
	"sync"

	pklgo "github.com/apple/pkl-go/pkl"
)

// helmReaderScheme is the Pkl URI scheme that the pkl-reader-helm binary
// advertises. The reader emits URIs like `reader+helm:/?request=...`,
// and the scheme passed to WithExternalResourceReader must match the
// full URI scheme (everything before the first `:`), including the
// `reader+` prefix. Pkl's resource allowlist is matched on this same
// string, so getting the scheme wrong silently blocks the read with
// an "allowlist" error rather than a "no reader" error.
const helmReaderScheme = "reader+helm"

// helmReaderBinaryName is the executable name we look for on PATH.
const helmReaderBinaryName = "pkl-reader-helm"

var (
	// helmReaderOnce is a pointer so tests can swap it for a fresh
	// sync.Once without tripping the copylocks vet check (sync.Once
	// contains a noCopy sentinel and can't be assigned by value).
	helmReaderOnce = &sync.Once{}
	helmReaderOpt  func(*pklgo.EvaluatorOptions)
)

// helmReaderOption returns a pkl-go option that registers the
// `reader+helm:` external resource reader when a `pkl-reader-helm`
// binary is reachable on PATH. Returns nil when no binary is found, so
// callers can append unconditionally — users without helm tooling pay
// nothing.
//
// PATH is the only discovery channel: opkg installs land the reader at
// the orbital tree's `bin/` (added to PATH by `ops setup`); package
// managers (brew, apt) install to a standard PATH location; manual
// installs document dropping the binary on PATH explicitly. Operators
// with unusual layouts can prepend a directory to PATH themselves.
//
// The lookup is cached for the process lifetime: PATH state doesn't
// change mid-run, and the alternative is exec.LookPath on every
// evaluator construction (twice per `formae apply`).
func helmReaderOption() func(*pklgo.EvaluatorOptions) {
	helmReaderOnce.Do(func() {
		path, err := exec.LookPath(helmReaderBinaryName)
		if err != nil {
			return
		}
		helmReaderOpt = pklgo.WithExternalResourceReader(helmReaderScheme, pklgo.ExternalReader{
			Executable: path,
		})
	})
	return helmReaderOpt
}

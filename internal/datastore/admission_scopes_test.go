// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package datastore

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdmissionStableScopeEncoding(t *testing.T) {
	require.Equal(t, "admission:stack:Abc_123-", AdmissionStackGuardKey("Abc_123-"))
	require.Equal(t, AdmissionStackGuardKey("Abc"), AdmissionStackGuardKey("Abc   "))
	for _, id := range []string{"", strings.Repeat("x", 129), "界", "tab\t"} {
		require.Equal(t, "admission:stack:exceptional", AdmissionStackGuardKey(id))
	}
}

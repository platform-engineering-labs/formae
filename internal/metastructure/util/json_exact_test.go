// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package util

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExactNumbersCompareDecimalValuesWithoutExpandingExponents(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		equal bool
	}{
		{`9007199254740992`, `9007199254740993`, false},
		{`9007199254740993`, `9.007199254740993e15`, true},
		{`1`, `1.000e+0`, true},
		{`-0`, `0.0e99`, true},
		{`0.001`, `1e-3`, true},
		{`1e99999999999999999999`, `10e99999999999999999998`, true},
		{`1e99999999999999999999`, `1e99999999999999999998`, false},
		{`1`, `"1"`, false},
	} {
		equal, err := JsonEqualIgnoreArrayOrderStrictRootsExactNumbers([]byte(tc.a), []byte(tc.b), nil)
		require.NoError(t, err)
		require.Equal(t, tc.equal, equal, "%s vs %s", tc.a, tc.b)
	}
	_, err := JsonEqualIgnoreArrayOrderStrictRootsExactNumbers([]byte(`1 2`), []byte(`1`), nil)
	require.Error(t, err, "lossless decoding must still reject trailing documents")
}

func TestStrictExactNumbersPreserveStructure(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		equal bool
	}{
		{`{"n":1}`, `{"n":1.0}`, true},
		{`[9007199254740993]`, `[9.007199254740993e15]`, true},
		{`[9007199254740992]`, `[9007199254740993]`, false},
		{`{"a":1,"b":2}`, `{"b":2.0,"a":1.0}`, true},
		{`[1,2]`, `[2,1]`, false},
		{`{}`, `{"n":null}`, false},
		{`{}`, `{"n":[]}`, false},
		{`{}`, `{"n":{}}`, false},
		{`null`, `[]`, false},
		{`1`, `"1"`, false},
	} {
		equal, err := JsonEqualExactNumbers([]byte(tc.a), []byte(tc.b))
		require.NoError(t, err)
		require.Equal(t, tc.equal, equal, "%s vs %s", tc.a, tc.b)
	}
	_, err := JsonEqualExactNumbers([]byte(`1 2`), []byte(`1`))
	require.Error(t, err)
}

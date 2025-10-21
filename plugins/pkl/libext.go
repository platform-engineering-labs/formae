// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"net/url"

	"github.com/apple/pkl-go/pkl"
	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib"
)

type libExtension struct{}

var _ pkl.ResourceReader = &libExtension{}

func (l libExtension) Scheme() string {
	return "libext"
}

func (l libExtension) HasHierarchicalUris() bool {
	return false
}

func (l libExtension) IsGlobbable() bool {
	return false
}

func (l libExtension) ListElements(_ url.URL) ([]pkl.PathElement, error) {
	return nil, nil
}

func (l libExtension) Read(uri url.URL) ([]byte, error) {
	return lib.Call(&uri).Json()
}

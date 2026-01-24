// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"errors"

	"github.com/platform-engineering-labs/formae/pkg/model"
)

// Handles loading and unloading for various schema plugins

const Schema Type = "schema"

var ErrFailedToGenerateSources = errors.New("failed to generate source code")

// SchemaLocation specifies where to resolve PKL schemas from
type SchemaLocation string

const (
	// SchemaLocationRemote resolves schemas from the PKL package registry (default)
	SchemaLocationRemote SchemaLocation = "remote"
	// SchemaLocationLocal resolves schemas from locally installed plugins (~/.pel/formae/plugins)
	SchemaLocationLocal SchemaLocation = "local"
)

type GenerateSourcesResult struct {
	TargetPath            string
	ProjectPath           string
	ResourceCount         int
	InitializedNewProject bool
	Warnings              []string
}

type SchemaPlugin interface {
	Name() string
	FileExtension() string
	SupportsExtract() bool
	FormaeConfig(path string) (*model.Config, error)
	Evaluate(path string, cmd model.Command, mode model.FormaApplyMode, props map[string]string) (*model.Forma, error)
	Serialize(resource *model.Resource, options *SerializeOptions) (string, error)
	SerializeForma(resources *model.Forma, options *SerializeOptions) (string, error)
	GenerateSourceCode(forma *model.Forma, targetPath string, includes []string, schemaLocation SchemaLocation) (GenerateSourcesResult, error)
	ProjectInit(path string, include []string, schemaLocation SchemaLocation) error
	ProjectProperties(path string) (map[string]model.Prop, error)
}

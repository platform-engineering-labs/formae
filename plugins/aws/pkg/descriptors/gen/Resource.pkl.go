// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Code generated from Pkl module `resource`. DO NOT EDIT.
package gen

import (
	"context"

	"github.com/apple/pkl-go/pkl"
)

type Resource struct {
	Resources []ResourceType `pkl:"resources"`
}

// LoadFromPath loads the pkl module at the given path and evaluates it into a Resource
func LoadFromPath(ctx context.Context, path string) (ret Resource, err error) {
	evaluator, err := pkl.NewEvaluator(ctx, pkl.PreconfiguredOptions)
	if err != nil {
		return ret, err
	}
	defer func() {
		cerr := evaluator.Close()
		if err == nil {
			err = cerr
		}
	}()
	ret, err = Load(ctx, evaluator, pkl.FileSource(path))
	return ret, err
}

// Load loads the pkl module at the given source and evaluates it with the given evaluator into a Resource
func Load(ctx context.Context, evaluator pkl.Evaluator, source *pkl.ModuleSource) (Resource, error) {
	var ret Resource
	err := evaluator.EvaluateModule(ctx, source, &ret)
	return ret, err
}

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package translator

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type Translator struct {
	datastore datastore.Datastore
}

func NewTranslator(ds datastore.Datastore) *Translator {
	return &Translator{
		datastore: ds,
	}
}

// ResolvableToFormaeURI translates a ResolvableObject to a FormaeURI by looking up the KSUID in the datastore
func (t *Translator) ResolvableToFormaeURI(resolvable pkgmodel.ResolvableObject) (pkgmodel.FormaeURI, error) {
	if resolvable.Label == "" || resolvable.Type == "" || resolvable.Stack == "" {
		return "", fmt.Errorf("resolvable object missing required fields: label=%s, type=%s, stack=%s",
			resolvable.Label, resolvable.Type, resolvable.Stack)
	}

	ksuid, err := t.datastore.GetKSUIDByTriplet(resolvable.Stack, resolvable.Label, resolvable.Type)
	if err != nil {
		return "", fmt.Errorf("failed to get KSUID for triplet %s/%s/%s: %w",
			resolvable.Stack, resolvable.Label, resolvable.Type, err)
	}
	if ksuid == "" {
		return "", fmt.Errorf("resource not found for triplet %s/%s/%s",
			resolvable.Stack, resolvable.Label, resolvable.Type)
	}

	return resolvable.ToFormaeURI(ksuid), nil
}

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package forma_command

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// NewFormaCommand stores the authenticated subject and its display-name hint
// alongside the existing device-attribution ClientID.
func TestNewFormaCommand_StoresSubjectAndSubjectName(t *testing.T) {
	fc := NewFormaCommand(
		&pkgmodel.Forma{},
		&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
		pkgmodel.CommandApply,
		nil, nil, nil, nil,
		"client-abc",
		"11111111-1111-4111-8111-111111111111",
		"dpanders",
		SourceUser,
	)

	assert.Equal(t, "client-abc", fc.ClientID)
	assert.Equal(t, "11111111-1111-4111-8111-111111111111", fc.Subject)
	assert.Equal(t, "dpanders", fc.SubjectName)
}

// NewFormaCommand leaves Subject and SubjectName empty when the caller has no
// authenticated identity to attribute (classic mode, or an internal origin).
func TestNewFormaCommand_EmptySubjectAndSubjectName(t *testing.T) {
	fc := NewFormaCommand(
		&pkgmodel.Forma{},
		&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
		pkgmodel.CommandApply,
		nil, nil, nil, nil,
		"",
		"",
		"",
		SourceAutoReconciler,
	)

	assert.Equal(t, "", fc.Subject)
	assert.Equal(t, "", fc.SubjectName)
}

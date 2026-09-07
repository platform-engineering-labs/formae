// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

type Ref struct {
	// What we're referencing. e.g. "formae://vpc.ec2.aws/stack/vpc-1#/VpcId"
	PropertyURI string

	ResourceURI FormaeURI

	// Property name on the source resource. e.g. "VpcId"
	SourcePropertyName string

	// Path in consuming resource. e.g. "NetworkConfig.VpcId"
	TargetPath string

	// Embedded marks a ref that lives inside a $embed field's $template string
	// (a framed span) rather than as a whole-value $ref object.
	Embedded bool
	// EmbedFieldPath is the dot-path of the enclosing $embed field (== TargetPath
	// for embedded refs); used at resolution to assemble the $template in place.
	EmbedFieldPath string

	// Resolved data
	ResolvedValue Value
}

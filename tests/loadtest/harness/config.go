// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package harness

import (
	"fmt"
	"time"
)

type ScaleProfile string

const (
	ProfileSmall  ScaleProfile = "small"
	ProfileMedium ScaleProfile = "medium"
	ProfileLarge  ScaleProfile = "large"
	ProfileXL     ScaleProfile = "xl"
)

type Config struct {
	AgentURL       string
	Profile        ScaleProfile
	SoakDuration   time.Duration
	ApplyTimeout   time.Duration
	DestroyTimeout time.Duration
	PhaseTimeout   time.Duration
	MimirURL       string
	TempoURL       string
	LokiURL        string
	FormaFilePath  string
	RunID          string
	GitSHA         string
}

type ProfileSpec struct {
	TotalResources int
	AWSResources   int
	AzureResources int
	GCPResources   int
	AgentVCPU      string
	AgentMemoryMB  int
	MaxDuration    time.Duration
}

var Profiles = map[ScaleProfile]ProfileSpec{
	ProfileSmall: {
		TotalResources: 500, AWSResources: 200, AzureResources: 150, GCPResources: 150,
		AgentVCPU: "1", AgentMemoryMB: 1024, MaxDuration: 30 * time.Minute,
	},
	ProfileMedium: {
		TotalResources: 5000, AWSResources: 2000, AzureResources: 1500, GCPResources: 1500,
		AgentVCPU: "2", AgentMemoryMB: 1024, MaxDuration: 90 * time.Minute,
	},
	ProfileLarge: {
		TotalResources: 20000, AWSResources: 8000, AzureResources: 6000, GCPResources: 6000,
		AgentVCPU: "4", AgentMemoryMB: 2048, MaxDuration: 180 * time.Minute,
	},
	ProfileXL: {
		TotalResources: 50000, AWSResources: 20000, AzureResources: 15000, GCPResources: 15000,
		AgentVCPU: "4", AgentMemoryMB: 4096, MaxDuration: 240 * time.Minute,
	},
}

func (c *Config) Validate() error {
	if c.AgentURL == "" {
		return fmt.Errorf("agent URL is required")
	}
	if _, ok := Profiles[c.Profile]; !ok {
		return fmt.Errorf("unknown profile: %s", c.Profile)
	}
	return nil
}

func (c *Config) ProfileSpec() ProfileSpec {
	return Profiles[c.Profile]
}

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/segmentio/ksuid"
)

const (
	ConfigFileNamePrefix = "formae.conf"
	ConfigDirectory      = ".config/formae"
	DataDirectory        = ".pel/formae"
)

var Config = cliconfig{}

type cliconfig struct{}

func (cliconfig) ConfigDirectory() string {
	homePath, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(homePath, ConfigDirectory)
}

func (cliconfig) DataDirectory() string {
	homePath, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(homePath, DataDirectory)
}

func (cliconfig) EnsureConfigDirectory() error {
	configPath := Config.ConfigDirectory()
	if configPath == "" {
		return fmt.Errorf("failed to ensure formae config directory")
	}

	return os.MkdirAll(configPath, 0700)
}

func (cliconfig) EnsureDataDirectory() error {
	dataPath := Config.DataDirectory()
	if dataPath == "" {
		return fmt.Errorf("failed to ensure formae data directory")
	}

	return os.MkdirAll(dataPath, 0700)
}

func (cliconfig) EnsureId(id string) error {
	configPath := Config.DataDirectory()
	if configPath == "" {
		return fmt.Errorf("failed to ensure formae directory")
	}

	idFile := filepath.Join(configPath, id)
	if _, err := os.Stat(idFile); os.IsNotExist(err) {
		err := os.WriteFile(idFile, []byte(ksuid.New().String()), 0600)
		if err != nil {
			return fmt.Errorf("failed to create ID file: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check ID file: %w", err)
	}

	return nil
}

func (cliconfig) EnsureClientID() error {
	return Config.EnsureId("cli_client_id")
}

func (cliconfig) EnsureAgentID() error {
	return Config.EnsureId("agent_id")
}

func (cliconfig) ClientID() (string, error) {
	configPath := Config.DataDirectory()
	if configPath == "" {
		return "", fmt.Errorf("failed to retrieve formae directory")
	}

	clientIDFile := filepath.Join(configPath, "cli_client_id")
	data, err := os.ReadFile(clientIDFile)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(data)), nil
}

func (cliconfig) AgentID() (string, error) {
	configPath := Config.DataDirectory()
	if configPath == "" {
		return "", fmt.Errorf("failed to retrieve formae directory")
	}

	agentIDFile := filepath.Join(configPath, "agent_id")
	data, err := os.ReadFile(agentIDFile)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(data)), nil
}

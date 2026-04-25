package providers

import (
	"fmt"
	"os"
	"strings"

	"github.com/gitgerby/lan-ipxe/driver-scrapers/core"
	"gopkg.in/yaml.v3"
)

// Config represents the top-level YAML structure.
type Config struct {
	Providers []ProviderConfig `yaml:"providers"`
}

// ProviderConfig represents a single provider definition in the YAML.
type ProviderConfig struct {
	Name    string         `yaml:"name"`
	Key     string         `yaml:"key"`
	Devices []DeviceConfig `yaml:"devices"`
}

// DeviceConfig represents a single device definition within a provider.
type DeviceConfig struct {
	Prefix            string   `yaml:"prefix"`
	HWID              string   `yaml:"hwid"`
	FamilyName        string   `yaml:"family_name"`
	Queries           []string `yaml:"queries"`
	PreferredBranches []string `yaml:"preferred_branches"`
	SelectionStrategy string   `yaml:"selection_strategy"`
	ExcludeNDIS       bool     `yaml:"exclude_ndis"`
}

// LoadProviders reads and parses the YAML configuration file at the given path.
// It validates the configuration and returns an error if anything is invalid.
func LoadProviders(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file %q: %w", path, err)
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validate config file %q: %w", path, err)
	}

	return &cfg, nil
}

// validate checks the configuration for correctness.
func validate(cfg *Config) error {
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("no providers defined")
	}

	seenKeys := make(map[string]bool)
	seenNames := make(map[string]bool)

	for i, p := range cfg.Providers {
		if p.Name == "" {
			return fmt.Errorf("provider at index %d: name is required", i)
		}
		if p.Key == "" {
			return fmt.Errorf("provider %q: key is required", p.Name)
		}
		if len(p.Devices) == 0 {
			return fmt.Errorf("provider %q (%s): at least one device is required", p.Name, p.Key)
		}
		if seenKeys[p.Key] {
			return fmt.Errorf("duplicate provider key %q", p.Key)
		}
		seenKeys[p.Key] = true
		if seenNames[p.Name] {
			return fmt.Errorf("duplicate provider name %q", p.Name)
		}
		seenNames[p.Name] = true

		seenDevicePrefixes := make(map[string]bool)
		for j, d := range p.Devices {
			if d.Prefix == "" {
				return fmt.Errorf("provider %q (%s): device at index %d: prefix is required", p.Name, p.Key, j)
			}
			if d.HWID == "" {
				return fmt.Errorf("provider %q (%s): device %q: hwid is required", p.Name, p.Key, d.Prefix)
			}
			if d.FamilyName == "" {
				return fmt.Errorf("provider %q (%s): device %q: family_name is required", p.Name, p.Key, d.Prefix)
			}
			if d.SelectionStrategy == "" {
				return fmt.Errorf("provider %q (%s): device %q: selection_strategy is required", p.Name, p.Key, d.Prefix)
			}
			if _, err := ParseSelectionStrategy(d.SelectionStrategy); err != nil {
				return fmt.Errorf("provider %q (%s): device %q: %w", p.Name, p.Key, d.Prefix, err)
			}
			if seenDevicePrefixes[d.Prefix] {
				return fmt.Errorf("provider %q (%s): duplicate device prefix %q", p.Name, p.Key, d.Prefix)
			}
			seenDevicePrefixes[d.Prefix] = true
		}
	}

	return nil
}

// ParseSelectionStrategy converts a string to a core.SelectionStrategy.
func ParseSelectionStrategy(s string) (core.SelectionStrategy, error) {
	switch strings.ToLower(s) {
	case "newest_by_date":
		return core.NewestByDate, nil
	case "semantic_version":
		return core.SemanticVersion, nil
	case "semantic_version_with_branch":
		return core.SemanticVersionWithBranch, nil
	default:
		return 0, fmt.Errorf("invalid selection strategy %q (valid: newest_by_date, semantic_version, semantic_version_with_branch)", s)
	}
}

// ToDeviceTarget converts a DeviceConfig to a core.DeviceTarget.
func (dc *DeviceConfig) ToDeviceTarget() (core.DeviceTarget, error) {
	strategy, err := ParseSelectionStrategy(dc.SelectionStrategy)
	if err != nil {
		return core.DeviceTarget{}, err
	}
	return core.DeviceTarget{
		Prefix:            dc.Prefix,
		HWID:              dc.HWID,
		FamilyName:        dc.FamilyName,
		Queries:           dc.Queries,
		PreferredBranches: dc.PreferredBranches,
		SelectionStrategy: strategy,
		ExcludeNDIS:       dc.ExcludeNDIS,
	}, nil
}

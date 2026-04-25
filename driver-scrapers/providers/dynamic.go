package providers

import (
	"github.com/gitgerby/lan-ipxe/driver-scrapers/core"
)

// dynamicProvider implements core.DriverProvider from a ProviderConfig.
type dynamicProvider struct {
	cfg     ProviderConfig
	devices []DeviceConfig
}

// NewDynamicProvider creates a DriverProvider from a ProviderConfig.
func NewDynamicProvider(cfg ProviderConfig) *dynamicProvider {
	devices := make([]DeviceConfig, len(cfg.Devices))
	copy(devices, cfg.Devices)
	return &dynamicProvider{
		cfg:     cfg,
		devices: devices,
	}
}

func (p *dynamicProvider) Name() string {
	return p.cfg.Name
}

func (p *dynamicProvider) ProviderKey() string {
	return p.cfg.Key
}

// Devices returns the devices as core.DeviceTarget (converting from DeviceConfig).
func (p *dynamicProvider) Devices() []core.DeviceTarget {
	result := make([]core.DeviceTarget, len(p.devices))
	for i, d := range p.devices {
		strategy, _ := ParseSelectionStrategy(d.SelectionStrategy)
		result[i] = core.DeviceTarget{
			Prefix:            d.Prefix,
			HWID:              d.HWID,
			FamilyName:        d.FamilyName,
			Queries:           d.Queries,
			PreferredBranches: d.PreferredBranches,
			SelectionStrategy: strategy,
			ExcludeNDIS:       d.ExcludeNDIS,
		}
	}
	return result
}

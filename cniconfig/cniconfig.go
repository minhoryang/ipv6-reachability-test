package cniconfig

import (
	"encoding/json"
	"fmt"
)

type ipamAddress struct {
	Address string `json:"address"`
}

type ipamConfig struct {
	Type      string        `json:"type"`
	Addresses []ipamAddress `json:"addresses"`
}

type plugin struct {
	Type   string     `json:"type"`
	Master string     `json:"master"`
	Mode   string     `json:"mode"`
	IPAM   ipamConfig `json:"ipam"`
}

type confList struct {
	CNIVersion string   `json:"cniVersion"`
	Name       string   `json:"name"`
	Plugins    []plugin `json:"plugins"`
}

// BuildConfList generates a CNI conflist JSON for an IPVLAN L2 network
// with static IPAM. addressCIDR should be like "fd00::abcd/64".
// Routes and DNS are NOT configured here — they are set up manually after
// CNI setup to support out-of-subnet gateways (which CNI IPAM can't handle).
func BuildConfList(name, master, addressCIDR string) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("network name must not be empty")
	}

	conf := confList{
		CNIVersion: "1.0.0",
		Name:       name,
		Plugins: []plugin{
			{
				Type:   "ipvlan",
				Master: master,
				Mode:   "l2",
				IPAM: ipamConfig{
					Type: "static",
					Addresses: []ipamAddress{
						{Address: addressCIDR},
					},
				},
			},
		},
	}

	return json.Marshal(conf)
}

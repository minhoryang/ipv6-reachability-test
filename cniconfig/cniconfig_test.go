package cniconfig

import (
	"encoding/json"
	"testing"
)

func TestBuildConfList_ValidJSON(t *testing.T) {
	data, err := BuildConfList("test-net", "eth0", "fd00::abcd/64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestBuildConfList_HasCorrectFields(t *testing.T) {
	data, err := BuildConfList("mynet", "eth0", "fd00::1234/64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var conf struct {
		CNIVersion string `json:"cniVersion"`
		Name       string `json:"name"`
		Plugins    []struct {
			Type   string `json:"type"`
			Master string `json:"master"`
			Mode   string `json:"mode"`
			IPAM   struct {
				Type      string `json:"type"`
				Addresses []struct {
					Address string `json:"address"`
				} `json:"addresses"`
			} `json:"ipam"`
		} `json:"plugins"`
	}

	if err := json.Unmarshal(data, &conf); err != nil {
		t.Fatalf("failed to parse conflist: %v", err)
	}

	if conf.CNIVersion != "1.0.0" {
		t.Errorf("expected cniVersion 1.0.0, got %s", conf.CNIVersion)
	}
	if conf.Name != "mynet" {
		t.Errorf("expected name mynet, got %s", conf.Name)
	}
	if len(conf.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(conf.Plugins))
	}

	p := conf.Plugins[0]
	if p.Type != "ipvlan" {
		t.Errorf("expected type ipvlan, got %s", p.Type)
	}
	if p.Master != "eth0" {
		t.Errorf("expected master eth0, got %s", p.Master)
	}
	if p.Mode != "l2" {
		t.Errorf("expected mode l2, got %s", p.Mode)
	}
	if p.IPAM.Type != "static" {
		t.Errorf("expected IPAM type static, got %s", p.IPAM.Type)
	}
	if len(p.IPAM.Addresses) != 1 || p.IPAM.Addresses[0].Address != "fd00::1234/64" {
		t.Errorf("unexpected address: %+v", p.IPAM.Addresses)
	}
}

func TestBuildConfList_EmptyName(t *testing.T) {
	_, err := BuildConfList("", "eth0", "fd00::1/64")
	if err == nil {
		t.Fatal("expected error for empty network name")
	}
}

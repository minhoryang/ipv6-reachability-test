package addrgen

import (
	"net"
	"testing"
)

func TestGenerate_ReturnsCorrectCount(t *testing.T) {
	addrs, err := Generate("fd00::/64", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(addrs) != 5 {
		t.Fatalf("expected 5 addresses, got %d", len(addrs))
	}
}

func TestGenerate_AddressesHaveCorrectPrefix(t *testing.T) {
	addrs, err := Generate("fd00::/64", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, subnet, _ := net.ParseCIDR("fd00::/64")
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("invalid IP: %s", addr)
		}
		if !subnet.Contains(ip) {
			t.Fatalf("address %s not in subnet %s", addr, subnet)
		}
	}
}

func TestGenerate_NoDuplicates(t *testing.T) {
	addrs, err := Generate("fd00::/64", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	seen := make(map[string]bool)
	for _, addr := range addrs {
		if seen[addr] {
			t.Fatalf("duplicate address: %s", addr)
		}
		seen[addr] = true
	}
}

func TestGatewayFromSubnet(t *testing.T) {
	gw, err := GatewayFromSubnet("fd00::/64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gw != "fd00::1" {
		t.Fatalf("expected fd00::1, got %s", gw)
	}
}

func TestGatewayFromSubnet_AnotherSubnet(t *testing.T) {
	gw, err := GatewayFromSubnet("fd01:db8::/64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gw != "fd01:db8::1" {
		t.Fatalf("expected fd01:db8::1, got %s", gw)
	}
}

func TestGatewayFromSubnet_InvalidSubnet(t *testing.T) {
	_, err := GatewayFromSubnet("not-a-subnet")
	if err == nil {
		t.Fatal("expected error for invalid subnet")
	}
}

func TestGenerate_InvalidSubnet(t *testing.T) {
	_, err := Generate("not-a-subnet", 5)
	if err == nil {
		t.Fatal("expected error for invalid subnet")
	}
}

func TestGenerate_ZeroCount(t *testing.T) {
	addrs, err := Generate("fd00::/64", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(addrs) != 0 {
		t.Fatalf("expected 0 addresses, got %d", len(addrs))
	}
}

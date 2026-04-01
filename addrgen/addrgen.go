package addrgen

import (
	"crypto/rand"
	"fmt"
	"net"
)

// GatewayFromSubnet returns the ::1 address for a given /64 subnet CIDR.
// For example, "fd00::/64" returns "fd00::1".
func GatewayFromSubnet(subnetCIDR string) (string, error) {
	_, subnet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return "", fmt.Errorf("invalid subnet CIDR: %w", err)
	}

	ones, bits := subnet.Mask.Size()
	if bits != 128 || ones != 64 {
		return "", fmt.Errorf("expected a /64 IPv6 subnet, got /%d", ones)
	}

	gw := make([]byte, 16)
	copy(gw, subnet.IP.To16())
	gw[15] = 1
	return net.IP(gw).String(), nil
}

// Generate produces n random IPv6 addresses within the given /64 subnet CIDR.
func Generate(subnetCIDR string, n int) ([]string, error) {
	ip, subnet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return nil, fmt.Errorf("invalid subnet CIDR: %w", err)
	}
	_ = ip

	ones, bits := subnet.Mask.Size()
	if bits != 128 || ones != 64 {
		return nil, fmt.Errorf("expected a /64 IPv6 subnet, got /%d", ones)
	}

	prefix := make([]byte, 8)
	copy(prefix, subnet.IP.To16()[:8])

	seen := make(map[string]bool)
	addrs := make([]string, 0, n)

	for len(addrs) < n {
		suffix := make([]byte, 8)
		if _, err := rand.Read(suffix); err != nil {
			return nil, fmt.Errorf("crypto/rand failed: %w", err)
		}

		full := make([]byte, 16)
		copy(full[:8], prefix)
		copy(full[8:], suffix)

		addr := net.IP(full).String()
		if seen[addr] {
			continue
		}
		seen[addr] = true
		addrs = append(addrs, addr)
	}

	return addrs, nil
}

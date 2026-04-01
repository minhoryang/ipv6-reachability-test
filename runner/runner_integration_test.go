//go:build integration

package runner

import (
	"context"
	"testing"
	"time"

	"github.com/minhoryang/ipv6-reachability-test/addrgen"
	"github.com/minhoryang/ipv6-reachability-test/cniconfig"
)

func TestRunner_SingleAddress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	r, err := New(ctx, Config{
		SocketPath: "/run/containerd/containerd.sock",
		Timeout:    10,
		CNIBinDir:  "/opt/cni/bin",
		CheckURL:   "http://ipaddr.io",
		Retries:    0,
	})
	if err != nil {
		t.Fatalf("failed to create runner: %v", err)
	}
	defer func() { _ = r.Close() }()

	testSubnet := "fd00::/64"
	addrs, err := addrgen.Generate(testSubnet, 1)
	if err != nil {
		t.Fatalf("failed to generate address: %v", err)
	}

	gw, err := addrgen.GatewayFromSubnet(testSubnet)
	if err != nil {
		t.Fatalf("failed to derive gateway: %v", err)
	}

	confList, err := cniconfig.BuildConfList("integ-test", "eth0", addrs[0]+"/64")
	if err != nil {
		t.Fatalf("failed to build conflist: %v", err)
	}

	result := r.Test(ctx, addrs[0], confList, gw)

	if result.Error != "" {
		t.Fatalf("test failed with error: %s", result.Error)
	}
	if !result.Pass {
		t.Fatalf("address mismatch: assigned %s, got %s", result.Address, result.ResponseIP)
	}

	t.Logf("PASS: %s responded as %s", result.Address, result.ResponseIP)
}

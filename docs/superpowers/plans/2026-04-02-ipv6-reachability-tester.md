# IPv6 /64 IPVLAN Reachability Tester — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go CLI that spot-checks IPv6 address reachability by creating IPVLAN containers via the containerd API and verifying correct source IP.

**Architecture:** Single Go binary with four internal packages: `addrgen` (random IPv6 generation), `cniconfig` (in-memory CNI conflist builder), `runner` (containerd + go-cni container lifecycle), and `main` (CLI flags + orchestration + reporting). Sequential test execution per address.

**Tech Stack:** Go 1.22+, `github.com/containerd/containerd/v2`, `github.com/containerd/go-cni`, `github.com/vishvananda/netns`, `github.com/opencontainers/runtime-spec`

---

## File Structure

```
.
├── go.mod
├── go.sum
├── main.go                    # CLI entry point, flags, orchestration, reporting
├── addrgen/
│   ├── addrgen.go             # IPv6 address generation within a /64 subnet
│   └── addrgen_test.go        # Unit tests for address generation
├── cniconfig/
│   ├── cniconfig.go           # In-memory CNI conflist JSON builder
│   └── cniconfig_test.go      # Unit tests for config generation
├── runner/
│   ├── runner.go              # Container lifecycle: create, exec, cleanup
│   └── runner_integration_test.go  # Integration test (build tag gated)
└── README.md                  # (existing)
```

---

### Task 1: Initialize Go Module

**Files:**
- Create: `go.mod`

- [x] **Step 1: Initialize the Go module**

Run:
```bash
cd /Users/minhoryang/PROJECTS/DN42/IPv6_64_ipvlan_reachability_tests
go mod init github.com/minhoryang/ipv6-reachability-test
```

Expected: `go.mod` created with module name.

- [x] **Step 2: Commit**

```bash
git init
git add go.mod
git commit -m "chore: initialize Go module"
```

---

### Task 2: IPv6 Address Generator — Tests

**Files:**
- Create: `addrgen/addrgen_test.go`

- [x] **Step 1: Create addrgen directory**

```bash
mkdir -p addrgen
```

- [x] **Step 2: Write failing tests for address generation**

Create `addrgen/addrgen_test.go`:

```go
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
```

- [x] **Step 3: Run tests to verify they fail**

Run: `go test ./addrgen/ -v`
Expected: FAIL — `Generate` not defined.

- [x] **Step 4: Commit**

```bash
git add addrgen/addrgen_test.go
git commit -m "test: add failing tests for IPv6 address generator"
```

---

### Task 3: IPv6 Address Generator — Implementation

**Files:**
- Create: `addrgen/addrgen.go`

- [x] **Step 1: Implement the address generator**

Create `addrgen/addrgen.go`:

```go
package addrgen

import (
	"crypto/rand"
	"fmt"
	"net"
)

// Generate produces n random IPv6 addresses within the given /64 subnet CIDR.
// Returns expanded IPv6 strings (no :: shorthand) for reliable comparison.
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
```

- [x] **Step 2: Run tests to verify they pass**

Run: `go test ./addrgen/ -v`
Expected: All 5 tests PASS.

- [x] **Step 3: Commit**

```bash
git add addrgen/addrgen.go
git commit -m "feat: implement IPv6 address generator for /64 subnets"
```

---

### Task 4: CNI Config Builder — Tests

**Files:**
- Create: `cniconfig/cniconfig_test.go`

- [x] **Step 1: Create cniconfig directory**

```bash
mkdir -p cniconfig
```

- [x] **Step 2: Write failing tests for CNI config builder**

Create `cniconfig/cniconfig_test.go`:

```go
package cniconfig

import (
	"encoding/json"
	"testing"
)

func TestBuildConfList_ValidJSON(t *testing.T) {
	data, err := BuildConfList("test-net", "eth0", "fd00::abcd/64", "fd00::1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestBuildConfList_HasCorrectFields(t *testing.T) {
	data, err := BuildConfList("mynet", "eth0", "fd00::1234/64", "fd00::1")
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
					Gateway string `json:"gateway"`
				} `json:"addresses"`
				Routes []struct {
					Dst string `json:"dst"`
					Gw  string `json:"gw"`
				} `json:"routes"`
				DNS struct {
					Nameservers []string `json:"nameservers"`
				} `json:"dns"`
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
	if p.IPAM.Addresses[0].Gateway != "fd00::1" {
		t.Errorf("unexpected gateway: %s", p.IPAM.Addresses[0].Gateway)
	}
	if len(p.IPAM.Routes) != 1 || p.IPAM.Routes[0].Dst != "::/0" {
		t.Errorf("unexpected routes: %+v", p.IPAM.Routes)
	}
	if len(p.IPAM.DNS.Nameservers) != 2 {
		t.Errorf("expected 2 nameservers, got %d", len(p.IPAM.DNS.Nameservers))
	}
}

func TestBuildConfList_EmptyName(t *testing.T) {
	_, err := BuildConfList("", "eth0", "fd00::1/64", "fd00::1")
	if err == nil {
		t.Fatal("expected error for empty network name")
	}
}
```

- [x] **Step 3: Run tests to verify they fail**

Run: `go test ./cniconfig/ -v`
Expected: FAIL — `BuildConfList` not defined.

- [x] **Step 4: Commit**

```bash
git add cniconfig/cniconfig_test.go
git commit -m "test: add failing tests for CNI config builder"
```

---

### Task 5: CNI Config Builder — Implementation

**Files:**
- Create: `cniconfig/cniconfig.go`

- [x] **Step 1: Implement the CNI config builder**

Create `cniconfig/cniconfig.go`:

```go
package cniconfig

import (
	"encoding/json"
	"fmt"
)

type ipamAddress struct {
	Address string `json:"address"`
	Gateway string `json:"gateway"`
}

type ipamRoute struct {
	Dst string `json:"dst"`
	Gw  string `json:"gw"`
}

type ipamDNS struct {
	Nameservers []string `json:"nameservers"`
}

type ipamConfig struct {
	Type      string        `json:"type"`
	Addresses []ipamAddress `json:"addresses"`
	Routes    []ipamRoute   `json:"routes"`
	DNS       ipamDNS       `json:"dns"`
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
func BuildConfList(name, master, addressCIDR, gateway string) ([]byte, error) {
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
						{Address: addressCIDR, Gateway: gateway},
					},
					Routes: []ipamRoute{
						{Dst: "::/0", Gw: gateway},
					},
					DNS: ipamDNS{
						Nameservers: []string{"2001:4860:4860::8888", "2001:4860:4860::8844"},
					},
				},
			},
		},
	}

	return json.Marshal(conf)
}
```

- [x] **Step 2: Run tests to verify they pass**

Run: `go test ./cniconfig/ -v`
Expected: All 3 tests PASS.

- [x] **Step 3: Commit**

```bash
git add cniconfig/cniconfig.go
git commit -m "feat: implement CNI conflist builder for IPVLAN L2"
```

---

### Task 6: Container Runner — Implementation

**Files:**
- Create: `runner/runner.go`

This is the core component. It uses containerd v2 client + go-cni + netns to run a container in a custom IPVLAN network and execute curl.

- [x] **Step 1: Fetch Go dependencies**

```bash
go get github.com/containerd/containerd/v2@latest
go get github.com/containerd/go-cni@latest
go get github.com/vishvananda/netns@latest
go get github.com/opencontainers/runtime-spec@latest
```

- [x] **Step 2: Create runner directory**

```bash
mkdir -p runner
```

- [x] **Step 3: Implement the container runner**

Create `runner/runner.go`:

```go
package runner

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"syscall"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	gocni "github.com/containerd/go-cni"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/vishvananda/netns"
)

// Result holds the outcome of a single address test.
type Result struct {
	Address    string
	Pass       bool
	ResponseIP string
	Error      string
}

// Runner manages containerd and CNI resources for running reachability tests.
type Runner struct {
	client    *containerd.Client
	image     containerd.Image
	timeout   int
	cniBinDir string
}

// New creates a Runner, connects to containerd, and pulls the alpine image.
func New(ctx context.Context, socketPath string, timeout int) (*Runner, error) {
	client, err := containerd.New(socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to containerd: %w", err)
	}

	nsCtx := namespaces.WithNamespace(ctx, "ipv6test")

	image, err := client.Pull(nsCtx, "docker.io/library/alpine:latest", containerd.WithPullUnpack)
	if err != nil {
		return nil, fmt.Errorf("pull alpine image: %w", err)
	}

	return &Runner{
		client:    client,
		image:     image,
		timeout:   timeout,
		cniBinDir: "/opt/cni/bin",
	}, nil
}

// Close releases the containerd client connection.
func (r *Runner) Close() {
	r.client.Close()
}

// Test runs a single reachability test for the given address using the provided
// CNI conflist JSON bytes, master interface, and gateway.
func (r *Runner) Test(ctx context.Context, address string, confListBytes []byte) Result {
	nsCtx := namespaces.WithNamespace(ctx, "ipv6test")
	containerID := fmt.Sprintf("ipv6test-%d", time.Now().UnixNano())
	netnsPath := fmt.Sprintf("/var/run/netns/%s", containerID)

	// Create network namespace
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNS, err := netns.Get()
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("get current netns: %v", err)}
	}
	defer origNS.Close()

	newNS, err := netns.NewNamed(containerID)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("create netns: %v", err)}
	}
	defer func() {
		netns.Set(origNS)
		newNS.Close()
		netns.DeleteNamed(containerID)
	}()
	netns.Set(origNS)

	// Setup CNI network in the new namespace
	cniLib, err := gocni.New(
		gocni.WithPluginDir([]string{r.cniBinDir}),
		gocni.WithConfListBytes(confListBytes),
		gocni.WithMinNetworkCount(1),
	)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("init CNI: %v", err)}
	}

	if err := cniLib.Load(gocni.WithLoNetwork); err != nil {
		return Result{Address: address, Error: fmt.Sprintf("load CNI: %v", err)}
	}

	_, err = cniLib.Setup(ctx, containerID, netnsPath)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("CNI setup: %v", err)}
	}
	defer cniLib.Remove(ctx, containerID, netnsPath)

	// Create container with the network namespace
	container, err := r.client.NewContainer(
		nsCtx,
		containerID,
		containerd.WithImage(r.image),
		containerd.WithNewSnapshot(containerID+"-snap", r.image),
		containerd.WithNewSpec(
			oci.WithImageConfig(r.image),
			oci.WithProcessArgs("sleep", "30"),
			oci.WithLinuxNamespace(specs.LinuxNamespace{
				Type: specs.NetworkNamespace,
				Path: netnsPath,
			}),
		),
	)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("create container: %v", err)}
	}
	defer container.Delete(nsCtx, containerd.WithSnapshotCleanup)

	// Create and start the main task (sleep)
	task, err := container.NewTask(nsCtx, cio.NullIO)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("create task: %v", err)}
	}
	defer func() {
		task.Kill(nsCtx, syscall.SIGKILL)
		task.Delete(nsCtx)
	}()

	exitCh, err := task.Wait(nsCtx)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("wait task: %v", err)}
	}
	_ = exitCh

	if err := task.Start(nsCtx); err != nil {
		return Result{Address: address, Error: fmt.Sprintf("start task: %v", err)}
	}

	// Exec wget inside the container (pre-installed in alpine)
	var stdout bytes.Buffer
	curlCmd := fmt.Sprintf("wget -q -O - --timeout=%d http://ipaddr.io", r.timeout)

	execProcess := &specs.Process{
		Args: []string{"sh", "-c", curlCmd},
		Env:  []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		Cwd:  "/",
	}

	execID := fmt.Sprintf("exec-%d", time.Now().UnixNano())
	execTask, err := task.Exec(nsCtx, execID, execProcess, cio.NewCreator(cio.WithStreams(nil, &stdout, nil)))
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("exec: %v", err)}
	}

	execExitCh, err := execTask.Wait(nsCtx)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("wait exec: %v", err)}
	}

	if err := execTask.Start(nsCtx); err != nil {
		return Result{Address: address, Error: fmt.Sprintf("start exec: %v", err)}
	}

	// Wait for exec to complete
	execStatus := <-execExitCh
	code, _, err := execStatus.Result()
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("exec result: %v", err)}
	}

	execTask.Delete(nsCtx)

	if code != 0 {
		return Result{Address: address, Error: fmt.Sprintf("curl exited with code %d", code)}
	}

	responseIP := strings.TrimSpace(stdout.String())
	pass := normalizeIPv6(responseIP) == normalizeIPv6(address)

	return Result{
		Address:    address,
		Pass:       pass,
		ResponseIP: responseIP,
	}
}

// normalizeIPv6 expands an IPv6 address to a canonical form for comparison.
func normalizeIPv6(addr string) string {
	ip := net.ParseIP(addr)
	if ip == nil {
		return addr
	}
	return ip.String()
}
```

- [x] **Step 4: Run `go mod tidy` to resolve dependencies**

```bash
go mod tidy
```

- [x] **Step 5: Run `go build ./...` to verify compilation**

Run: `go build ./...`
Expected: No errors.

- [x] **Step 6: Commit**

```bash
git add runner/ go.mod go.sum
git commit -m "feat: implement container runner with containerd + go-cni"
```

---

### Task 7: Main CLI — Implementation

**Files:**
- Create: `main.go`

- [x] **Step 1: Implement the main CLI**

Create `main.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/minhoryang/ipv6-reachability-test/addrgen"
	"github.com/minhoryang/ipv6-reachability-test/cniconfig"
	"github.com/minhoryang/ipv6-reachability-test/runner"
)

func main() {
	count := flag.Int("n", 10, "number of random addresses to test")
	flag.IntVar(count, "count", 10, "number of random addresses to test")
	master := flag.String("m", "eth0", "host interface for IPVLAN")
	flag.StringVar(master, "master", "eth0", "host interface for IPVLAN")
	subnet := flag.String("s", "fd00::/64", "IPv6 subnet to test")
	flag.StringVar(subnet, "subnet", "fd00::/64", "IPv6 subnet to test")
	gateway := flag.String("g", "", "gateway address (default: <subnet>::1)")
	flag.StringVar(gateway, "gateway", "", "gateway address (default: <subnet>::1)")
	timeout := flag.Int("timeout", 10, "request timeout in seconds")
	socket := flag.String("socket", "/run/containerd/containerd.sock", "containerd socket path")
	flag.Parse()

	if *count < 0 {
		fmt.Fprintf(os.Stderr, "Error: count must be non-negative\n")
		os.Exit(1)
	}

	// Auto-derive gateway from subnet if not specified
	if *gateway == "" {
		gw, err := addrgen.GatewayFromSubnet(*subnet)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error deriving gateway from subnet: %v\n", err)
			os.Exit(1)
		}
		*gateway = gw
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Generate random addresses
	addrs, err := addrgen.Generate(*subnet, *count)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating addresses: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Testing %d random addresses in %s\n", *count, *subnet)

	// Create runner
	r, err := runner.New(ctx, *socket, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing runner: %v\n", err)
		os.Exit(1)
	}
	defer r.Close()

	// Run tests
	passed := 0
	failed := 0

	for i, addr := range addrs {
		// Build CNI config for this address
		netName := fmt.Sprintf("ipv6test-%d", i)
		confList, err := cniconfig.BuildConfList(netName, *master, addr+"/64", *gateway)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s ... FAIL (config error: %v)\n", i+1, *count, addr, err)
			failed++
			continue
		}

		result := r.Test(ctx, addr, confList)

		if result.Pass {
			fmt.Printf("[%d/%d] %s ... PASS\n", i+1, *count, addr)
			passed++
		} else {
			reason := result.Error
			if reason == "" {
				reason = fmt.Sprintf("got %s", result.ResponseIP)
			}
			fmt.Printf("[%d/%d] %s ... FAIL (%s)\n", i+1, *count, addr, reason)
			failed++
		}

		// Check for cancellation
		select {
		case <-ctx.Done():
			fmt.Println("\nInterrupted.")
			os.Exit(1)
		default:
		}
	}

	// Summary
	fmt.Println()
	if failed == 0 {
		fmt.Printf("Result: %d/%d passed\n", passed, passed+failed)
	} else {
		fmt.Printf("Result: %d/%d passed, %d failed\n", passed, passed+failed, failed)
	}

	if failed > 0 {
		os.Exit(1)
	}
}
```

- [x] **Step 2: Run `go build .` to verify compilation**

Run: `go build -o ipv6-reachability-test .`
Expected: Binary created successfully.

- [x] **Step 3: Commit**

```bash
git add main.go
git commit -m "feat: implement CLI with flags, orchestration, and reporting"
```

---

### Task 8: Integration Test

**Files:**
- Create: `runner/runner_integration_test.go`

- [x] **Step 1: Write the integration test**

Create `runner/runner_integration_test.go`:

```go
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

	r, err := New(ctx, "/run/containerd/containerd.sock", 10)
	if err != nil {
		t.Fatalf("failed to create runner: %v", err)
	}
	defer r.Close()

	testSubnet := "fd00::/64"
	addrs, err := addrgen.Generate(testSubnet, 1)
	if err != nil {
		t.Fatalf("failed to generate address: %v", err)
	}

	gw, err := addrgen.GatewayFromSubnet(testSubnet)
	if err != nil {
		t.Fatalf("failed to derive gateway: %v", err)
	}

	confList, err := cniconfig.BuildConfList("integ-test", "eth0", addrs[0]+"/64", gw)
	if err != nil {
		t.Fatalf("failed to build conflist: %v", err)
	}

	result := r.Test(ctx, addrs[0], confList)

	if result.Error != "" {
		t.Fatalf("test failed with error: %s", result.Error)
	}
	if !result.Pass {
		t.Fatalf("address mismatch: assigned %s, got %s", result.Address, result.ResponseIP)
	}

	t.Logf("PASS: %s responded as %s", result.Address, result.ResponseIP)
}
```

- [x] **Step 2: Verify it compiles (but don't run — requires real containerd)**

Run: `go build ./runner/`
Expected: No errors.

- [x] **Step 3: Commit**

```bash
git add runner/runner_integration_test.go
git commit -m "test: add integration test for runner (build-tag gated)"
```

---

### Task 9: Final Tidy and Verify

**Files:**
- Modify: `go.mod`, `go.sum`

- [x] **Step 1: Run go mod tidy**

```bash
go mod tidy
```

- [x] **Step 2: Run all unit tests**

Run: `go test ./addrgen/ ./cniconfig/ -v`
Expected: All tests PASS.

- [x] **Step 3: Run go vet**

Run: `go vet ./...`
Expected: No issues.

- [x] **Step 4: Verify build**

Run: `go build -o ipv6-reachability-test .`
Expected: Binary built successfully.

- [x] **Step 5: Commit any remaining changes**

```bash
git add go.mod go.sum
git commit -m "chore: tidy go modules"
```

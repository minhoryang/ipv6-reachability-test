package runner

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	gocni "github.com/containerd/go-cni"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/vishvananda/netns"
)

// withNetnsPath returns an OCI spec option that sets (or replaces) the network
// namespace to use the given path. Unlike oci.WithLinuxNamespace which appends,
// this removes any existing network namespace entry first, ensuring the container
// joins our pre-created netns instead of creating a new one.
func withNetnsPath(path string) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		if s.Linux == nil {
			s.Linux = &specs.Linux{}
		}
		// Remove existing network namespace entries
		filtered := s.Linux.Namespaces[:0]
		for _, ns := range s.Linux.Namespaces {
			if ns.Type != specs.NetworkNamespace {
				filtered = append(filtered, ns)
			}
		}
		// Add ours with the explicit path
		s.Linux.Namespaces = append(filtered, specs.LinuxNamespace{
			Type: specs.NetworkNamespace,
			Path: path,
		})
		return nil
	}
}

// Global atomic counter for unique container IDs (safe under parallelism).
var containerSeq atomic.Int64

// Config holds all configurable parameters for creating a Runner.
type Config struct {
	SocketPath string
	Timeout    int
	CNIBinDir  string
	CheckURL   string
	Retries    int
}

// Result holds the outcome of a single address test.
type Result struct {
	Address    string `json:"address"`
	Pass       bool   `json:"pass"`
	ResponseIP string `json:"response_ip,omitempty"`
	Error      string `json:"error,omitempty"`
	Attempts   int    `json:"attempts"`
}

// Runner manages containerd and CNI resources for running reachability tests.
type Runner struct {
	client    *containerd.Client
	image     containerd.Image
	timeout   int
	cniBinDir string
	checkURL  string
	retries   int
}

// New creates a Runner, connects to containerd, and pulls the alpine image.
func New(ctx context.Context, cfg Config) (*Runner, error) {
	client, err := containerd.New(cfg.SocketPath)
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
		timeout:   cfg.Timeout,
		cniBinDir: cfg.CNIBinDir,
		checkURL:  cfg.CheckURL,
		retries:   cfg.Retries,
	}, nil
}

// Close releases the containerd client connection.
func (r *Runner) Close() error {
	return r.client.Close()
}

// setupRoutes configures IPv6 routing inside a network namespace.
// This is done manually (not via CNI) because CNI IPAM cannot handle
// out-of-subnet gateways — it tries to add routes "via gateway" even for
// the host route to the gateway itself, creating a circular dependency.
// ipvlanIfName is the interface name go-cni assigns to the IPVLAN network.
// go-cni names interfaces as eth{index} where index is the load order.
// We load WithLoNetwork first (lo), then WithConfListBytes second → eth1.
const ipvlanIfName = "eth1"

func setupRoutes(ctx context.Context, nsName, gateway string) error {
	// Add host route to gateway (makes it reachable even if outside the /64)
	out, err := exec.CommandContext(ctx, "ip", "netns", "exec", nsName,
		"ip", "-6", "route", "add", gateway+"/128", "dev", ipvlanIfName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("add gateway host route: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// Add default route via gateway
	out, err = exec.CommandContext(ctx, "ip", "netns", "exec", nsName,
		"ip", "-6", "route", "add", "default", "via", gateway, "dev", ipvlanIfName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("add default route: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// TestWithRetry runs Test() up to r.retries+1 times, returning on first pass
// or after all attempts are exhausted.
func (r *Runner) TestWithRetry(ctx context.Context, address string, confListBytes []byte, gateway string) Result {
	var result Result
	for attempt := 0; attempt <= r.retries; attempt++ {
		select {
		case <-ctx.Done():
			return Result{Address: address, Error: "context cancelled", Attempts: attempt}
		default:
		}

		result = r.Test(ctx, address, confListBytes, gateway)
		result.Attempts = attempt + 1
		if result.Pass {
			return result
		}

		// Backoff before retry (skip on last attempt)
		if attempt < r.retries {
			select {
			case <-ctx.Done():
				result.Attempts = attempt + 1
				return result
			case <-time.After(time.Duration(attempt+1) * time.Second):
			}
		}
	}
	return result
}

// printManualCleanup prints shell commands to manually recover leaked resources.
// It prints commands for the given step and all subsequent steps that would have run.
func printManualCleanup(containerID string, from string) {
	steps := []struct {
		name string
		cmd  string
	}{
		{"kill task", fmt.Sprintf("ctr -n ipv6test task kill %s", containerID)},
		{"delete task", fmt.Sprintf("ctr -n ipv6test task rm %s", containerID)},
		{"delete container", fmt.Sprintf("ctr -n ipv6test container rm %s", containerID)},
		{"delete snapshot", fmt.Sprintf("ctr -n ipv6test snapshot rm %s-snap", containerID)},
		{"delete netns", fmt.Sprintf("ip netns del %s", containerID)},
	}

	started := false
	log.Printf("Manual cleanup commands for %s:", containerID)
	for _, s := range steps {
		if s.name == from {
			started = true
		}
		if started {
			log.Printf("  # %s", s.name)
			log.Printf("  %s", s.cmd)
		}
	}
}

// Test runs a single reachability test for the given address using the provided
// CNI conflist JSON bytes.
func (r *Runner) Test(ctx context.Context, address string, confListBytes []byte, gateway string) Result {
	// Use context.WithoutCancel for cleanup operations so defers succeed after SIGINT.
	cleanupCtx := context.WithoutCancel(ctx)
	nsCtx := namespaces.WithNamespace(ctx, "ipv6test")
	nsCleanupCtx := namespaces.WithNamespace(cleanupCtx, "ipv6test")

	seq := containerSeq.Add(1)
	containerID := fmt.Sprintf("ipv6test-%d", seq)
	netnsPath := fmt.Sprintf("/var/run/netns/%s", containerID)

	// Create network namespace
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNS, err := netns.Get()
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("get current netns: %v", err)}
	}
	defer func() {
		if err := origNS.Close(); err != nil {
			log.Printf("warning: failed to close origNS fd: %v", err)
		}
	}()

	newNS, err := netns.NewNamed(containerID)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("create netns: %v", err)}
	}
	defer func() {
		// CRITICAL: restoring the original netns is mandatory.
		// If this fails, the goroutine (and its locked OS thread) is stuck in the
		// wrong namespace, corrupting process state. Print cleanup commands and halt.
		if err := netns.Set(origNS); err != nil {
			printManualCleanup(containerID, "delete netns")
			log.Fatalf("FATAL: failed to restore original netns: %v — process state is corrupt, exiting", err)
		}
		if err := newNS.Close(); err != nil {
			log.Printf("warning: failed to close netns fd for %s: %v", containerID, err)
		}
		if err := netns.DeleteNamed(containerID); err != nil {
			log.Printf("warning: leaked netns %s: %v", containerID, err)
			log.Printf("  manual fix: sudo ip netns del %s", containerID)
		}
	}()

	// Restore original netns immediately (we only needed to create the named one)
	if err := netns.Set(origNS); err != nil {
		printManualCleanup(containerID, "delete netns")
		log.Fatalf("FATAL: failed to restore original netns after creation: %v — process state is corrupt, exiting", err)
	}

	// Setup CNI network in the new namespace
	cniLib, err := gocni.New(
		gocni.WithPluginDir([]string{r.cniBinDir}),
		gocni.WithMinNetworkCount(1),
	)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("init CNI: %v", err)}
	}

	if err := cniLib.Load(gocni.WithLoNetwork, gocni.WithConfListBytes(confListBytes)); err != nil {
		return Result{Address: address, Error: fmt.Sprintf("load CNI: %v", err)}
	}

	_, err = cniLib.Setup(ctx, containerID, netnsPath)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("CNI setup: %v", err)}
	}
	defer func() {
		if err := cniLib.Remove(cleanupCtx, containerID, netnsPath); err != nil {
			log.Printf("warning: CNI remove failed for %s: %v", containerID, err)
		}
	}()

	// Set up routes manually (CNI IPAM can't handle out-of-subnet gateways)
	if err := setupRoutes(ctx, containerID, gateway); err != nil {
		return Result{Address: address, Error: fmt.Sprintf("setup routes: %v", err)}
	}

	// Create container with the network namespace
	container, err := r.client.NewContainer(
		nsCtx,
		containerID,
		containerd.WithImage(r.image),
		containerd.WithNewSnapshot(containerID+"-snap", r.image),
		containerd.WithNewSpec(
			oci.WithImageConfig(r.image),
			oci.WithProcessArgs("sleep", fmt.Sprintf("%d", r.timeout+30)),
			withNetnsPath(netnsPath),
		),
	)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("create container: %v", err)}
	}
	defer func() {
		if err := container.Delete(nsCleanupCtx, containerd.WithSnapshotCleanup); err != nil {
			log.Printf("warning: container delete failed for %s: %v", containerID, err)
			log.Printf("  manual fix: sudo ctr -n ipv6test container rm %s", containerID)
			log.Printf("  manual fix: sudo ctr -n ipv6test snapshot rm %s-snap", containerID)
		}
	}()

	// Create and start the main task (sleep)
	task, err := container.NewTask(nsCtx, cio.NullIO)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("create task: %v", err)}
	}
	exitCh, err := task.Wait(nsCtx)
	if err != nil {
		return Result{Address: address, Error: fmt.Sprintf("wait task: %v", err)}
	}

	defer func() {
		if err := task.Kill(nsCleanupCtx, syscall.SIGKILL); err != nil {
			log.Printf("warning: task kill failed for %s: %v", containerID, err)
		}
		// Wait for the task to actually stop before deleting
		select {
		case <-exitCh:
		case <-time.After(5 * time.Second):
			log.Printf("warning: timeout waiting for task %s to stop", containerID)
		}
		if _, err := task.Delete(nsCleanupCtx); err != nil {
			log.Printf("warning: task delete failed for %s: %v", containerID, err)
			printManualCleanup(containerID, "kill task")
		}
	}()

	if err := task.Start(nsCtx); err != nil {
		return Result{Address: address, Error: fmt.Sprintf("start task: %v", err)}
	}

	// Exec wget inside the container
	// Write resolv.conf first — CNI static IPAM dns block is metadata only,
	// the ipvlan plugin does not write /etc/resolv.conf.
	var stdout, stderr bytes.Buffer
	wgetCmd := fmt.Sprintf(
		"echo 'nameserver 2001:4860:4860::8888' > /etc/resolv.conf && sleep 2 && wget -O - --timeout=%d %s",
		r.timeout, r.checkURL,
	)

	execProcess := &specs.Process{
		Args: []string{"sh", "-c", wgetCmd},
		Env:  []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		Cwd:  "/",
	}

	execSeq := containerSeq.Add(1)
	execID := fmt.Sprintf("exec-%d", execSeq)
	execTask, err := task.Exec(nsCtx, execID, execProcess, cio.NewCreator(cio.WithStreams(bytes.NewReader(nil), &stdout, &stderr)))
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

	if _, err := execTask.Delete(nsCleanupCtx); err != nil {
		log.Printf("warning: exec task delete failed for %s: %v", containerID, err)
	}

	if code != 0 {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return Result{Address: address, Error: fmt.Sprintf("wget exited with code %d: %s", code, errMsg)}
		}
		return Result{Address: address, Error: fmt.Sprintf("wget exited with code %d", code)}
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

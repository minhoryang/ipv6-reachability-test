package runner

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"syscall"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	gocni "github.com/containerd/go-cni"
	"github.com/vishvananda/netns"
)

// TempShell creates a container with the same IPVLAN network setup and runs
// an interactive shell (or a given command) attached to the user's terminal.
// It blocks until the command exits.
func (r *Runner) TempShell(ctx context.Context, address string, confListBytes []byte, gateway string, args []string) error {
	cleanupCtx := context.WithoutCancel(ctx)
	nsCtx := namespaces.WithNamespace(ctx, "ipv6test")
	nsCleanupCtx := namespaces.WithNamespace(cleanupCtx, "ipv6test")

	seq := containerSeq.Add(1)
	containerID := fmt.Sprintf("ipv6test-%d", seq)
	netnsPath := fmt.Sprintf("/var/run/netns/%s", containerID)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNS, err := netns.Get()
	if err != nil {
		return fmt.Errorf("get current netns: %w", err)
	}
	defer func() {
		if err := origNS.Close(); err != nil {
			log.Printf("warning: failed to close origNS fd: %v", err)
		}
	}()

	newNS, err := netns.NewNamed(containerID)
	if err != nil {
		return fmt.Errorf("create netns: %w", err)
	}
	defer func() {
		if err := netns.Set(origNS); err != nil {
			printManualCleanup(containerID, "delete netns")
			log.Fatalf("FATAL: failed to restore original netns: %v", err)
		}
		if err := newNS.Close(); err != nil {
			log.Printf("warning: failed to close netns fd for %s: %v", containerID, err)
		}
		if err := netns.DeleteNamed(containerID); err != nil {
			log.Printf("warning: leaked netns %s: %v", containerID, err)
			log.Printf("  manual fix: ip netns del %s", containerID)
		}
	}()

	if err := netns.Set(origNS); err != nil {
		printManualCleanup(containerID, "delete netns")
		log.Fatalf("FATAL: failed to restore original netns: %v", err)
	}

	// Setup CNI
	cniLib, err := gocni.New(
		gocni.WithPluginDir([]string{r.cniBinDir}),
		gocni.WithMinNetworkCount(1),
	)
	if err != nil {
		return fmt.Errorf("init CNI: %w", err)
	}
	if err := cniLib.Load(gocni.WithLoNetwork, gocni.WithConfListBytes(confListBytes)); err != nil {
		return fmt.Errorf("load CNI: %w", err)
	}
	if _, err = cniLib.Setup(ctx, containerID, netnsPath); err != nil {
		return fmt.Errorf("CNI setup: %w", err)
	}
	defer func() {
		if err := cniLib.Remove(cleanupCtx, containerID, netnsPath); err != nil {
			log.Printf("warning: CNI remove failed for %s: %v", containerID, err)
		}
	}()

	// Set up routes manually (CNI IPAM can't handle out-of-subnet gateways)
	if err := setupRoutes(ctx, containerID, gateway); err != nil {
		return fmt.Errorf("setup routes: %w", err)
	}

	// Determine command to run
	shellArgs := args
	if len(shellArgs) == 0 {
		shellArgs = []string{"/bin/sh"}
	}

	// Create container
	container, err := r.client.NewContainer(
		nsCtx,
		containerID,
		containerd.WithImage(r.image),
		containerd.WithNewSnapshot(containerID+"-snap", r.image),
		containerd.WithNewSpec(
			oci.WithImageConfig(r.image),
			oci.WithProcessArgs(shellArgs...),
			withNetnsPath(netnsPath),
		),
	)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	defer func() {
		if err := container.Delete(nsCleanupCtx, containerd.WithSnapshotCleanup); err != nil {
			log.Printf("warning: container delete failed for %s: %v", containerID, err)
			printManualCleanup(containerID, "delete container")
		}
	}()

	// Create task with terminal IO attached
	task, err := container.NewTask(nsCtx, cio.NewCreator(cio.WithStreams(os.Stdin, os.Stdout, os.Stderr)))
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	exitCh, err := task.Wait(nsCtx)
	if err != nil {
		return fmt.Errorf("wait task: %w", err)
	}
	defer func() {
		if err := task.Kill(nsCleanupCtx, syscall.SIGKILL); err != nil {
			// May already be exited
			log.Printf("warning: task kill: %v", err)
		}
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
		return fmt.Errorf("start task: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Container %s ready (address: %s). Press Ctrl+D to exit.\n", containerID, address)

	// Wait for the task to exit
	status := <-exitCh
	code, _, err := status.Result()
	if err != nil {
		return fmt.Errorf("task result: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("shell exited with code %d", code)
	}
	return nil
}

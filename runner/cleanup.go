package runner

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

const (
	cleanupPrefix    = "ipv6test-"
	cleanupNamespace = "ipv6test"
	netnsDir         = "/var/run/netns"
)

// Cleanup removes all leftover ipv6test-* resources: tasks, containers, snapshots, and netns.
func Cleanup(ctx context.Context, socketPath string) []error {
	var errs []error

	// Clean containerd resources
	cErrs := cleanupContainerd(ctx, socketPath)
	errs = append(errs, cErrs...)

	// Clean leftover network namespaces
	nErrs := cleanupNetns()
	errs = append(errs, nErrs...)

	return errs
}

func cleanupContainerd(ctx context.Context, socketPath string) []error {
	var errs []error

	client, err := containerd.New(socketPath)
	if err != nil {
		return []error{fmt.Errorf("connect to containerd: %w", err)}
	}
	defer func() { _ = client.Close() }()

	nsCtx := namespaces.WithNamespace(ctx, cleanupNamespace)

	// List and kill/delete tasks, then containers
	containers, err := client.Containers(nsCtx)
	if err != nil {
		return []error{fmt.Errorf("list containers: %w", err)}
	}

	for _, c := range containers {
		id := c.ID()
		if !strings.HasPrefix(id, cleanupPrefix) {
			continue
		}

		// Try to get and clean up the task
		task, err := c.Task(nsCtx, nil)
		if err == nil {
			log.Printf("killing task %s", id)
			if err := task.Kill(nsCtx, syscall.SIGKILL); err != nil {
				log.Printf("  kill failed (may already be stopped): %v", err)
			}

			// Wait for task to stop
			exitCh, err := task.Wait(nsCtx)
			if err == nil {
				select {
				case <-exitCh:
				case <-time.After(5 * time.Second):
					log.Printf("  timeout waiting for task %s to stop", id)
				}
			}

			log.Printf("deleting task %s", id)
			if _, err := task.Delete(nsCtx); err != nil {
				errs = append(errs, fmt.Errorf("delete task %s: %w", id, err))
			}
		}

		log.Printf("deleting container %s (with snapshot cleanup)", id)
		if err := c.Delete(nsCtx, containerd.WithSnapshotCleanup); err != nil {
			errs = append(errs, fmt.Errorf("delete container %s: %w", id, err))
			// Try deleting snapshot separately
			snapName := id + "-snap"
			log.Printf("  attempting direct snapshot delete: %s", snapName)
			out, sErr := exec.CommandContext(ctx, "ctr", "-n", cleanupNamespace, "snapshot", "rm", snapName).CombinedOutput()
			if sErr != nil {
				errs = append(errs, fmt.Errorf("delete snapshot %s: %s %w", snapName, strings.TrimSpace(string(out)), sErr))
			}
		}
	}

	return errs
}

func cleanupNetns() []error {
	var errs []error

	entries, err := os.ReadDir(netnsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []error{fmt.Errorf("read %s: %w", netnsDir, err)}
	}

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), cleanupPrefix) {
			continue
		}
		nsName := entry.Name()
		path := filepath.Join(netnsDir, nsName)
		log.Printf("deleting netns %s", nsName)

		// Use ip netns del (handles unmount + remove portably)
		out, err := exec.Command("ip", "netns", "del", nsName).CombinedOutput()
		if err != nil {
			log.Printf("  ip netns del failed: %s", strings.TrimSpace(string(out)))
			// Fallback: try direct remove
			if rmErr := os.Remove(path); rmErr != nil {
				errs = append(errs, fmt.Errorf("remove netns %s: %w", nsName, rmErr))
			}
		}
	}

	return errs
}

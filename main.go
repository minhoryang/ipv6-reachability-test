package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/minhoryang/ipv6-reachability-test/addrgen"
	"github.com/minhoryang/ipv6-reachability-test/cniconfig"
	"github.com/minhoryang/ipv6-reachability-test/runner"
)

var cli struct {
	Run       RunCmd       `cmd:"" default:"withargs" help:"Run reachability tests (default)."`
	Cleanup   CleanupCmd   `cmd:"" help:"Remove all leftover ipv6test-* resources from a previous run."`
	Tempshell TempshellCmd `cmd:"" help:"Launch a temporary shell inside a networked container for debugging."`
}

type RunCmd struct {
	Count     int    `short:"n" default:"10" help:"Number of random addresses to test."`
	Master    string `short:"m" default:"eth0" help:"Host interface for IPVLAN."`
	Subnet    string `short:"s" default:"fd00::/64" help:"IPv6 /64 subnet to test."`
	Gateway   string `short:"g" default:"" help:"Gateway address (default: <subnet>::1)."`
	Parallel  int    `short:"j" default:"1" help:"Number of parallel test workers."`
	Retries   int    `short:"r" default:"0" help:"Number of retries on failure."`
	JSON      bool   `help:"Output results as JSON."`
	CheckURL  string `name:"check-url" default:"http://ipaddr.io" help:"URL that returns the caller's IP address."`
	Timeout   int    `default:"10" help:"Request timeout in seconds."`
	CNIBinDir string `name:"cni-bin-dir" default:"/opt/cni/bin" help:"Directory containing CNI plugin binaries."`
	Socket    string `default:"/run/containerd/containerd.sock" help:"containerd socket path."`
}

type CleanupCmd struct {
	Socket string `default:"/run/containerd/containerd.sock" help:"containerd socket path."`
}

type TempshellCmd struct {
	Count     int      `short:"n" hidden:"" default:"0"`
	Master    string   `short:"m" default:"eth0" help:"Host interface for IPVLAN."`
	Subnet    string   `short:"s" default:"fd00::/64" help:"IPv6 /64 subnet to test."`
	Gateway   string   `short:"g" default:"" help:"Gateway address (default: <subnet>::1)."`
	Parallel  int      `short:"j" hidden:"" default:"1"`
	Retries   int      `short:"r" hidden:"" default:"0"`
	JSON      bool     `hidden:""`
	Timeout   int      `default:"10" help:"Request timeout in seconds."`
	CNIBinDir string   `name:"cni-bin-dir" default:"/opt/cni/bin" help:"Directory containing CNI plugin binaries."`
	Socket    string   `default:"/run/containerd/containerd.sock" help:"containerd socket path."`
	CheckURL  string   `name:"check-url" default:"http://ipaddr.io" help:"URL that returns the caller's IP address."`
	Args      []string `arg:"" optional:"" help:"Command to run (default: /bin/sh)."`
}

func (cmd *TempshellCmd) Run() error {
	if cmd.Gateway == "" {
		gw, err := addrgen.GatewayFromSubnet(cmd.Subnet)
		if err != nil {
			return fmt.Errorf("deriving gateway: %w", err)
		}
		cmd.Gateway = gw
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	addrs, err := addrgen.Generate(cmd.Subnet, 1)
	if err != nil {
		return fmt.Errorf("generating address: %w", err)
	}
	addr := addrs[0]

	r, err := runner.New(ctx, runner.Config{
		SocketPath: cmd.Socket,
		Timeout:    cmd.Timeout,
		CNIBinDir:  cmd.CNIBinDir,
		CheckURL:   cmd.CheckURL,
	})
	if err != nil {
		return fmt.Errorf("initializing runner: %w", err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			log.Printf("warning: failed to close runner: %v", err)
		}
	}()

	confList, err := cniconfig.BuildConfList("ipv6test-shell", cmd.Master, addr+"/64")
	if err != nil {
		return fmt.Errorf("building CNI config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Launching shell with address %s\n", addr)
	return r.TempShell(ctx, addr, confList, cmd.Gateway, cmd.Args)
}

type jsonOutput struct {
	Subnet  string          `json:"subnet"`
	Gateway string          `json:"gateway"`
	Count   int             `json:"count"`
	Passed  int             `json:"passed"`
	Failed  int             `json:"failed"`
	Results []runner.Result `json:"results"`
}

func (cmd *CleanupCmd) Run() error {
	ctx := context.Background()
	errs := runner.Cleanup(ctx, cmd.Socket)
	if len(errs) > 0 {
		for _, e := range errs {
			log.Printf("cleanup error: %v", e)
		}
		return fmt.Errorf("%d cleanup errors", len(errs))
	}
	fmt.Println("Cleanup complete.")
	return nil
}

func (cmd *RunCmd) Run() error {
	if cmd.Count < 0 {
		return fmt.Errorf("count must be non-negative")
	}
	if cmd.Parallel < 1 {
		return fmt.Errorf("parallel must be at least 1")
	}

	// Auto-derive gateway from subnet if not specified
	if cmd.Gateway == "" {
		gw, err := addrgen.GatewayFromSubnet(cmd.Subnet)
		if err != nil {
			return fmt.Errorf("deriving gateway from subnet: %w", err)
		}
		cmd.Gateway = gw
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Generate random addresses
	addrs, err := addrgen.Generate(cmd.Subnet, cmd.Count)
	if err != nil {
		return fmt.Errorf("generating addresses: %w", err)
	}

	if !cmd.JSON {
		fmt.Printf("Testing %d random addresses in %s (parallel=%d)\n", cmd.Count, cmd.Subnet, cmd.Parallel)
	}

	// Create runner
	r, err := runner.New(ctx, runner.Config{
		SocketPath: cmd.Socket,
		Timeout:    cmd.Timeout,
		CNIBinDir:  cmd.CNIBinDir,
		CheckURL:   cmd.CheckURL,
		Retries:    cmd.Retries,
	})
	if err != nil {
		return fmt.Errorf("initializing runner: %w", err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			log.Printf("warning: failed to close runner: %v", err)
		}
	}()

	// Run tests with worker pool
	type indexedResult struct {
		index  int
		result runner.Result
	}

	resultCh := make(chan indexedResult, len(addrs))
	sem := make(chan struct{}, cmd.Parallel)
	var wg sync.WaitGroup

	for i, addr := range addrs {
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		sem <- struct{}{} // acquire semaphore slot

		go func(idx int, address string) {
			defer wg.Done()
			defer func() { <-sem }()

			netName := fmt.Sprintf("ipv6test-%d", idx)
			confList, err := cniconfig.BuildConfList(netName, cmd.Master, address+"/64")
			if err != nil {
				resultCh <- indexedResult{
					index:  idx,
					result: runner.Result{Address: address, Error: fmt.Sprintf("config error: %v", err), Attempts: 1},
				}
				return
			}

			result := r.TestWithRetry(ctx, address, confList, cmd.Gateway)
			resultCh <- indexedResult{index: idx, result: result}
		}(i, addr)
	}

	// Close result channel when all workers are done
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results
	results := make([]runner.Result, len(addrs))
	passed := 0
	failed := 0

	for ir := range resultCh {
		results[ir.index] = ir.result
		if ir.result.Pass {
			passed++
		} else {
			failed++
		}

		if !cmd.JSON {
			if ir.result.Pass {
				fmt.Printf("[%d/%d] %s ... PASS", ir.index+1, cmd.Count, ir.result.Address)
			} else {
				reason := ir.result.Error
				if reason == "" {
					reason = fmt.Sprintf("got %s", ir.result.ResponseIP)
				}
				fmt.Printf("[%d/%d] %s ... FAIL (%s)", ir.index+1, cmd.Count, ir.result.Address, reason)
			}
			if ir.result.Attempts > 1 {
				fmt.Printf(" [%d attempts]", ir.result.Attempts)
			}
			fmt.Println()
		}
	}

	// Handle interruption message
	if ctx.Err() != nil && !cmd.JSON {
		fmt.Println("\nInterrupted. In-flight tests completed cleanup.")
	}

	// Output
	if cmd.JSON {
		output := jsonOutput{
			Subnet:  cmd.Subnet,
			Gateway: cmd.Gateway,
			Count:   passed + failed,
			Passed:  passed,
			Failed:  failed,
			Results: results[:passed+failed],
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(output); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		}
	} else {
		fmt.Println()
		if failed == 0 {
			fmt.Printf("Result: %d/%d passed\n", passed, passed+failed)
		} else {
			fmt.Printf("Result: %d/%d passed, %d failed\n", passed, passed+failed, failed)
		}
	}

	if failed > 0 {
		os.Exit(1)
	}
	return nil
}

func main() {
	kctx := kong.Parse(&cli,
		kong.Name("ipv6-reachability-test"),
		kong.Description("Spot-check IPv6 /64 subnet reachability via containerd + IPVLAN."),
	)
	if err := kctx.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

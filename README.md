# ipv6-reachability-test

Spot-check that random IPv6 addresses within a /64 subnet are globally reachable and not NATed.

Spins up isolated containers via containerd, assigns each a random address using IPVLAN L2 networking, then verifies the source IP seen by an external service matches the assigned address.

## How it works

1. Generate N random IPv6 addresses within the target /64 subnet
2. For each address, create a network namespace with IPVLAN L2 + static IPAM
3. Launch an Alpine container attached to that namespace
4. `wget` an IP echo service from inside the container
5. Compare the response (external view of source IP) to the assigned address
6. Report pass/fail per address

Supports parallel execution, automatic retries, and JSON output.

## Requirements

- Linux with IPv6 connectivity
- [containerd](https://containerd.io/) running (`/run/containerd/containerd.sock`)
- [CNI plugins](https://github.com/containernetworking/plugins) installed at `/opt/cni/bin`
- Root privileges (for network namespaces and containerd access)

## Install

```bash
go build -o ipv6-reachability-test .
```

## Usage

```bash
# Test 10 random addresses in fd00::/64 (default)
sudo ./ipv6-reachability-test

# Test a specific subnet — gateway auto-derived as ::1
sudo ./ipv6-reachability-test -s fd00:1234::/64

# Parallel with retries and JSON output
sudo ./ipv6-reachability-test -s fd00:1234::/64 -n 20 -j 4 --retries 2 --json

# All options
sudo ./ipv6-reachability-test \
  -n 20 \
  -s fd00:1234::/64 \
  -g fd00:1234::1 \
  -m eth0 \
  -j 4 \
  -r 1 \
  --json \
  --check-url http://ipaddr.io \
  --timeout 10 \
  --cni-bin-dir /opt/cni/bin \
  --socket /run/containerd/containerd.sock
```

### Flags

| Short | Long | Default | Description |
|-------|------|---------|-------------|
| `-n` | `--count` | `10` | Number of random addresses to test |
| `-s` | `--subnet` | `fd00::/64` | IPv6 /64 subnet to test |
| `-g` | `--gateway` | `<subnet>::1` | Gateway address (auto-derived if omitted) |
| `-m` | `--master` | `eth0` | Host interface for IPVLAN |
| `-j` | `--parallel` | `1` | Number of parallel test workers |
| `-r` | `--retries` | `0` | Number of retries on failure |
| | `--json` | `false` | Output results as JSON |
| | `--check-url` | `http://ipaddr.io` | URL that returns the caller's IP address |
| | `--timeout` | `10` | HTTP request timeout in seconds |
| | `--cni-bin-dir` | `/opt/cni/bin` | Directory containing CNI plugin binaries |
| | `--socket` | `/run/containerd/containerd.sock` | containerd socket path |

### Example output

```
Testing 3 random addresses in fd00::/64 (parallel=1)
[1/3] fd00::a1b2:c3d4:e5f6:7890 ... PASS
[2/3] fd00::1234:5678:9abc:def0 ... PASS
[3/3] fd00::dead:beef:cafe:babe ... FAIL (got fd00::1)

Result: 2/3 passed, 1 failed
```

With `--json`:

```json
{
  "subnet": "fd00::/64",
  "gateway": "fd00::1",
  "count": 3,
  "passed": 2,
  "failed": 1,
  "results": [
    {"address": "fd00::a1b2:c3d4:e5f6:7890", "pass": true, "response_ip": "fd00::a1b2:c3d4:e5f6:7890", "attempts": 1},
    {"address": "fd00::1234:5678:9abc:def0", "pass": true, "response_ip": "fd00::1234:5678:9abc:def0", "attempts": 1},
    {"address": "fd00::dead:beef:cafe:babe", "pass": false, "response_ip": "fd00::1", "attempts": 1}
  ]
}
```

Exit code is `0` if all tests pass, `1` if any fail.

### Graceful shutdown

On SIGINT/SIGTERM, the tool stops launching new tests but waits for in-flight tests to complete cleanup (containers, network namespaces, CNI networks) before exiting. No orphaned resources are left behind.

## Project structure

```
.
├── main.go          # CLI entry point, parallel orchestration, JSON output
├── addrgen/         # Random IPv6 address generation within /64
├── cniconfig/       # CNI conflist builder (IPVLAN L2 + static IPAM)
└── runner/          # Container lifecycle (containerd + go-cni + netns)
```

## Running tests

```bash
# Unit tests
go test ./addrgen/ ./cniconfig/

# Integration test (requires containerd + CNI + root)
sudo go test -tags integration ./runner/ -v
```

## License

MIT

# IPv6 /64 IPVLAN Reachability Tester — Design Spec

## Overview

A Go CLI tool that spot-checks IPv6 address reachability within a /64 subnet by creating IPVLAN containers via the containerd API, verifying each container can reach the public internet and that the reported source IP matches the assigned address.

Runs directly on a DN42 node with containerd installed.

## Architecture

Single Go binary with these components:

1. **Address Generator** — produces N random IPv6 addresses within the target /64 subnet
2. **CNI Config Builder** — generates in-memory IPVLAN L2 network configs with static IPAM
3. **Container Manager** — uses containerd Go client to pull images, create/start/exec/cleanup containers
4. **Result Reporter** — collects pass/fail per address, prints summary

Flow per test address (sequential):
1. Generate CNI config with the address (in-memory, no files)
2. Create network namespace, setup CNI via go-cni
3. Create container in that namespace
4. Exec `wget -q -O - --timeout=<timeout> http://ipaddr.io`
5. Compare response IP to assigned address (normalized)
6. Cleanup (defers): kill task, delete container+snapshot, CNI Remove, delete netns

## IPv6 Address Generation

- Fixed prefix: upper 64 bits extracted from the provided /64 subnet (default `fd00::`)
- Random interface identifier: 8 bytes from `crypto/rand` (lower 64 bits)
- Each address uses /64 mask
- Gateway: auto-derived as `<subnet>::1` via `GatewayFromSubnet()`, or manually specified
- Duplicate detection within a single run (regenerate if collision)

## CNI Network Config

Generated in-memory per test address. No files written to `/etc/cni/net.d/`.

```json
{
  "cniVersion": "1.0.0",
  "name": "ipv6test-<short-random>",
  "plugins": [
    {
      "type": "ipvlan",
      "master": "<master-interface>",
      "mode": "l2",
      "ipam": {
        "type": "static",
        "addresses": [
          {
            "address": "<generated-address>/64",
            "gateway": "<gateway>"
          }
        ],
        "routes": [
          { "dst": "::/0", "gw": "<gateway>" }
        ],
        "dns": {
          "nameservers": ["2001:4860:4860::8888", "2001:4860:4860::8844"]
        }
      }
    }
  ]
}
```

## Container & Network Lifecycle

- **Image**: `docker.io/library/alpine:latest` (wget is pre-installed). Pulled once at startup, reused for all tests.
- **containerd client**: Connects to local socket (`/run/containerd/containerd.sock`).
- **CNI config**: Generated in-memory via `gocni.WithConfListBytes()` — no files written to disk.
- **Per test**:
  1. Create unique network namespace (`netns.NewNamed`)
  2. Setup CNI network in the namespace via go-cni (`cniLib.Setup`)
  3. Create container with the namespace via containerd client
  4. Start container (sleep 30), exec `wget -q -O - --timeout=<timeout> http://ipaddr.io`
  5. Capture stdout
  6. Cleanup (always runs, via defers): delete exec, kill+delete task, delete container+snapshot, CNI Remove, delete named netns

Sequential execution — no parallelism. A run of 10 addresses takes ~1-2 minutes.

## Validation

- Trim whitespace from wget response
- Normalize both IPs via `net.ParseIP().String()` for canonical comparison
- PASS: response matches assigned address
- FAIL: mismatch (log expected vs actual), timeout, or wget error

## CLI Interface

```
ipv6-reachability-test [flags]

Flags:
  -n, --count int        Number of random addresses to test (default 10)
  -m, --master string    Host interface for IPVLAN (default "eth0")
  -s, --subnet string    IPv6 subnet to test (default "fd00::/64")
  -g, --gateway string   Gateway address (default: <subnet>::1, auto-derived)
      --timeout int      HTTP request timeout in seconds (default 10)
      --socket string    containerd socket path (default "/run/containerd/containerd.sock")
```

## Output

```
Testing 10 random addresses in fd00::/64
[1/10] fd00:a1b2:c3d4:e5f6:7890 ... PASS
[2/10] fd00:1234:5678:9abc:def0 ... PASS
[3/10] fd00:aaaa:bbbb:cccc:dddd ... FAIL (got fd00::1)
...
Result: 8/10 passed, 2 failed
```

Exit code: 0 if all pass, 1 if any fail.

## Error Handling

| Scenario | Behavior |
|---|---|
| containerd not running | Fail fast: "cannot connect to containerd socket" |
| Image pull failure | Fail fast: report network/image issue |
| CNI setup failure | Log address as FAIL with reason, continue to next |
| Curl timeout | Log as FAIL (timeout), continue |
| Curl returns wrong IP | Log as FAIL (expected vs actual), continue |
| Cleanup failure | Log warning, continue (best-effort) |

Tolerant of individual test failures. Fails fast on systemic issues.

## Dependencies

- `github.com/containerd/containerd/v2` — container lifecycle (v2 client API)
- `github.com/containerd/go-cni` — CNI network setup/teardown (in-memory conflist)
- `github.com/vishvananda/netns` — network namespace creation/management
- `github.com/opencontainers/runtime-spec` — OCI spec types for container/exec config
- `crypto/rand`, `net` — address generation (stdlib)

## Testing Strategy

- **Unit tests**: Address generation (correct prefix, uniqueness, valid IPv6), CNI config building (valid JSON structure, correct fields), result parsing (match, mismatch, timeout cases)
- **Integration test**: Gated behind `//go:build integration`. Runs 1 address through the full lifecycle on a real containerd + IPVLAN host. No mocks.

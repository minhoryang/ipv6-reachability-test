# TIL (Today I Learned)

## containerd OCI spec: WithLinuxNamespace appends, doesn't replace

`oci.WithLinuxNamespace()` **appends** to the namespace list. If `oci.WithImageConfig()` already added a network namespace with an empty path (meaning "create new"), you end up with two entries — and the container gets a fresh netns, ignoring yours.

**Fix:** Remove existing network namespace entries before adding yours:

```go
func withNetnsPath(path string) oci.SpecOpts {
    return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
        filtered := s.Linux.Namespaces[:0]
        for _, ns := range s.Linux.Namespaces {
            if ns.Type != specs.NetworkNamespace {
                filtered = append(filtered, ns)
            }
        }
        s.Linux.Namespaces = append(filtered, specs.LinuxNamespace{
            Type: specs.NetworkNamespace,
            Path: path,
        })
        return nil
    }
}
```

## containerd exec: nil streams cause shim crash

`cio.NewCreator(cio.WithStreams(nil, &stdout, nil))` causes containerd-shim to try opening empty file paths. Always provide all three streams:

```go
cio.WithStreams(bytes.NewReader(nil), &stdout, &stderr)
```

## containerd task.Kill() is async

`task.Kill(ctx, SIGKILL)` returns immediately. Calling `task.Delete()` right after races — you get "task must be stopped before deletion". Wait on the exit channel between kill and delete:

```go
task.Kill(ctx, syscall.SIGKILL)
select {
case <-exitCh:
case <-time.After(5 * time.Second):
}
task.Delete(ctx)
```

## CNI static IPAM dns block is metadata only

The `dns.nameservers` field in a CNI conflist with `ipvlan` plugin does **not** write `/etc/resolv.conf`. You must do it yourself inside the container:

```sh
echo 'nameserver 2001:4860:4860::8888' > /etc/resolv.conf
```

## CNI plugin binary locations vary by distro

| Path | Distro |
|------|--------|
| `/opt/cni/bin` | Upstream default, Docker, k8s |
| `/usr/lib/cni` | Debian, Ubuntu |
| `/usr/libexec/cni` | Fedora, RHEL, CentOS |

## context.WithoutCancel for cleanup defers

When SIGINT cancels the parent context, cleanup defers that use that context will fail (containerd operations return "context canceled"). Use `context.WithoutCancel(ctx)` (Go 1.21+) for cleanup operations:

```go
cleanupCtx := context.WithoutCancel(ctx)
defer container.Delete(cleanupCtx, ...)
```

## go-cni Load() resets networks added in New()

`gocni.New(gocni.WithConfListBytes(bytes))` appends the network to `c.networks`. But `cniLib.Load(gocni.WithLoNetwork)` calls `c.reset()` first, wiping all networks, then only adds loopback. The ipvlan network silently disappears — CNI reports success but only `lo` exists.

**Fix:** Pass `WithConfListBytes` to `Load()`, not `New()`:

```go
cniLib, _ := gocni.New(
    gocni.WithPluginDir(dirs),
    gocni.WithMinNetworkCount(1),
)
cniLib.Load(gocni.WithLoNetwork, gocni.WithConfListBytes(confListBytes))
```

## netns.Set() failure is unrecoverable

If `netns.Set(origNS)` fails after working in a different namespace, the goroutine's OS thread is stuck in the wrong netns. The process state is corrupt — `log.Fatalf` and print manual cleanup commands is the only safe option.

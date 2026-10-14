# End-to-end test

Runs fest against a real Arch Linux system instead of mocked command output.
The test boots a stock Arch VM with Vagrant, builds and applies the
configuration in `config/`, checks the resulting system state, then applies a
second time to confirm the configuration is idempotent.

Unlike the unit tests, nothing here is faked: real pacman, systemd,
timedatectl and localectl.

## Prerequisites

- [Vagrant](https://developer.hashicorp.com/vagrant/docs/installation)
- A Vagrant provider, VirtualBox by default, or libvirt (`--provider=libvirt`)

The first run downloads the `generic/arch` box and upgrades packages in the VM,
so it takes several minutes.

## Running

```bash
go test -tags e2e -timeout 45m -v ./e2e/
```

`-timeout` matters: the default 10m is not enough for the first boot.

Leave the VM running for debugging (it is torn down otherwise):

```bash
FEST_E2E_KEEP=1 go test -tags e2e -timeout 45m -v ./e2e/
vagrant ssh            # from e2e/
```

## Layout

| File | Purpose |
| --- | --- |
| `e2e_test.go` | Driver: boots the VM, applies the config, runs the checks |
| `Vagrantfile` | Box, provider settings, uploads the repo tarball |
| `provision.sh` | Installs fest's dependencies and unpacks the repository |
| `verify.sh` | Asserts the system state inside the VM |
| `config/main.go` | The fest configuration under test |
| `config/system/` | System files deployed by the config |
| `config/user/` | Dotfiles stowed by the config |

`config/main.go` and `verify.sh` are two halves of the same contract: every
declaration in one has a matching assertion in the other. Keep them in sync.

## CI

`.github/workflows/ci.yml` runs build, vet and the unit tests in an
`archlinux` container (fest links libalpm via cgo, so it cannot build on the
Ubuntu runner directly).

This end-to-end test is **not** part of CI: it needs a hypervisor, so it has to
run on a machine with Vagrant and VirtualBox installed and `vboxdrv` loaded. Run
it manually:

```bash
go test -tags e2e -timeout 45m -v ./e2e/
```


#!/usr/bin/env bash
# Provisions the fest end-to-end VM. Runs as the unprivileged vagrant user,
# which has passwordless sudo. The repository tarball was uploaded by Vagrant
# (see Vagrantfile and e2e_test.go).
set -euo pipefail

# The box ships with a stale keyring, so a plain -Syu fails with "required key
# missing from keyring". Refresh the keyring before upgrading.
sudo pacman-key --init
sudo pacman-key --populate archlinux
sudo pacman -Sy --noconfirm archlinux-keyring
sudo pacman-key --populate archlinux

# fest links libalpm through cgo, so it needs a C toolchain (base-devel) and the
# pacman package, which ships /usr/include/alpm.h and /usr/lib/libalpm.so.
# go, expac, stow and fd are fest's required runtime dependencies.
sudo pacman -Syu --noconfirm --needed base-devel go expac stow fd

# The box ships /etc/resolv.conf with no nameserver and relies on
# systemd-resolved through nsswitch. libc (pacman) resolves fine, but Go's
# built-in resolver reads /etc/resolv.conf directly and fails, so point it at
# the systemd-resolved stub.
sudo ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf

# Unpack the repository uploaded by the test.
rm -rf "$HOME/fest"
mkdir -p "$HOME/fest"
tar -xzf /tmp/fest-e2e-src.tar.gz -C "$HOME/fest"

# Build once here so a compile failure surfaces during provisioning rather than
# halfway through the test.
cd "$HOME/fest"
go build ./...

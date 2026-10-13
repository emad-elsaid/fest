#!/usr/bin/env bash
# Asserts the state left behind by `fest apply` inside the end-to-end VM.
# Run by e2e_test.go; exits non-zero if any expectation is not met.
set -uo pipefail

failed=0
ok()  { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; failed=1; }

check() { # description actual expected
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi
}

check_contains() { # description haystack needle
  case "$2" in
    *"$3"*) ok "$1" ;;
    *) bad "$1 ('$2' does not contain '$3')" ;;
  esac
}

check_group() { # description user group
  if id -nG "$2" | tr ' ' '\n' | grep -qx "$3"; then ok "$1"; else bad "$1 ('$2' is not in group '$3')"; fi
}

# --- packages (fest.Package) ---
check "package tree installed"   "$(pacman -Qq tree   2>/dev/null)" "tree"
check "package jq installed"     "$(pacman -Qq jq     2>/dev/null)" "jq"
check "package cronie installed" "$(pacman -Qq cronie 2>/dev/null)" "cronie"

# --- time / locale / keyboard (fest.Timedate / fest.Locale / fest.Keyboard) ---
check "timezone is Asia/Tokyo" "$(timedatectl show -p Timezone --value)" "Asia/Tokyo"
check "NTP is enabled"         "$(timedatectl show -p NTP --value)" "yes"
check_contains "system locale"     "$(localectl status)" "LANG=en_US.UTF-8"
check_contains "console keymap"    "$(localectl status)" "us"
check "locale generated"           "$(locale -a | grep -ix 'en_US.utf8' || true)" "en_US.utf8"
check_contains "x11 keyboard options" "$(cat /etc/X11/xorg.conf.d/00-keyboard.conf 2>/dev/null)" "ctrl:nocaps"

# --- systemd service (fest.SystemService): cronie is disabled on a stock install ---
check "cronie enabled" "$(systemctl is-enabled cronie.service 2>/dev/null)" "enabled"
check "cronie active"  "$(systemctl is-active  cronie.service 2>/dev/null)" "active"

# --- user groups (fest.Group): the stock vagrant user is not in video ---
check_group "vagrant joined video group" vagrant video

# --- system files (the system/ tree) ---
check "managed system file content" "$(cat /etc/fest-e2e.conf 2>/dev/null)" "managed by fest e2e"
check "managed system file mode"    "$(stat -c %a /etc/fest-e2e.conf 2>/dev/null)" "644"

# --- dotfiles (stow, the user/ tree) ---
check "dotfile content" "$(cat "$HOME/.fest-e2e-dotfile" 2>/dev/null)" "managed by fest e2e"
check "dotfile is a stow symlink" "$(readlink -f "$HOME/.fest-e2e-dotfile" 2>/dev/null)" \
  "$HOME/fest/e2e/config/user/.fest-e2e-dotfile"

if [ "$failed" -ne 0 ]; then
  echo
  echo "e2e verification FAILED"
  exit 1
fi
echo
echo "e2e verification passed"

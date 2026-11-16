// SPDX-License-Identifier: GPL-2.0-or-later
//
// This file is a Go port of pacman 7.1.0's lib/libalpm/version.c, which in
// turn adapted rpmvercmp and parseEVR from RPM 4.8.1.
// Copyright (c) 2006-2025 Pacman Development Team <pacman-dev@lists.archlinux.org>
// Licensed under the GNU General Public License, version 2 or later.

package alpm

import "strings"

// VerCmp compares two pacman version strings. It returns a value less than
// zero if v1 is older than v2, zero if they are equal, and greater than zero
// if v1 is newer. It is a Go port of libalpm's alpm_pkg_vercmp.
func VerCmp(v1, v2 string) int {
	if v1 == v2 {
		return 0
	}

	epoch1, version1, release1, hasRelease1 := parseEVR(v1)
	epoch2, version2, release2, hasRelease2 := parseEVR(v2)

	if ret := rpmvercmp(epoch1, epoch2); ret != 0 {
		return ret
	}

	if ret := rpmvercmp(version1, version2); ret != 0 {
		return ret
	}

	if hasRelease1 && hasRelease2 {
		return rpmvercmp(release1, release2)
	}

	return 0
}

// parseEVR splits an [epoch:]version[-release] string into its parts.
func parseEVR(evr string) (epoch, version, release string, hasRelease bool) {
	s := 0
	for s < len(evr) && isDigit(evr[s]) {
		s++
	}

	// Find the last '-' at or after the epoch terminator, matching strrchr(s, '-').
	se := -1
	if idx := strings.LastIndexByte(evr[s:], '-'); idx >= 0 {
		se = s + idx
	}

	if s < len(evr) && evr[s] == ':' {
		epoch = evr[:s]
		if epoch == "" {
			epoch = "0"
		}

		version = evr[s+1:]
		if se >= 0 {
			version = evr[s+1 : se]
			release = evr[se+1:]
			hasRelease = true
		}

		return epoch, version, release, hasRelease
	}

	epoch = "0"
	version = evr
	if se >= 0 {
		version = evr[:se]
		release = evr[se+1:]
		hasRelease = true
	}

	return epoch, version, release, hasRelease
}

// rpmvercmp compares two version segments the way libalpm's rpmvercmp does.
func rpmvercmp(a, b string) int {
	if a == b {
		return 0
	}

	one, two := 0, 0
	ptr1, ptr2 := 0, 0

	for one < len(a) && two < len(b) {
		for one < len(a) && !isAlnum(a[one]) {
			one++
		}
		for two < len(b) && !isAlnum(b[two]) {
			two++
		}

		if one >= len(a) || two >= len(b) {
			break
		}

		if one-ptr1 != two-ptr2 {
			if one-ptr1 < two-ptr2 {
				return -1
			}

			return 1
		}

		ptr1, ptr2 = one, two

		isnum := isDigit(a[ptr1])
		if isnum {
			for ptr1 < len(a) && isDigit(a[ptr1]) {
				ptr1++
			}
			for ptr2 < len(b) && isDigit(b[ptr2]) {
				ptr2++
			}
		} else {
			for ptr1 < len(a) && isAlpha(a[ptr1]) {
				ptr1++
			}
			for ptr2 < len(b) && isAlpha(b[ptr2]) {
				ptr2++
			}
		}

		if one == ptr1 {
			return -1
		}

		if two == ptr2 {
			if isnum {
				return 1
			}

			return -1
		}

		if isnum {
			for one < ptr1 && a[one] == '0' {
				one++
			}
			for two < ptr2 && b[two] == '0' {
				two++
			}

			if ptr1-one > ptr2-two {
				return 1
			}
			if ptr2-two > ptr1-one {
				return -1
			}
		}

		segA, segB := a[one:ptr1], b[two:ptr2]
		if segA != segB {
			if segA < segB {
				return -1
			}

			return 1
		}

		one, two = ptr1, ptr2
	}

	if one >= len(a) && two >= len(b) {
		return 0
	}

	if (one >= len(a) && !isAlpha(b[two])) || (one < len(a) && isAlpha(a[one])) {
		return -1
	}

	return 1
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isAlnum(c byte) bool { return isDigit(c) || isAlpha(c) }

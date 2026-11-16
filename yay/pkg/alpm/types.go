// SPDX-License-Identifier: GPL-2.0-or-later
//
// Dependency parsing and matching are Go ports of pacman 7.1.0's
// lib/libalpm/deps.c.
// Copyright (c) 2006-2025 Pacman Development Team <pacman-dev@lists.archlinux.org>

package alpm

import "strings"

// Depend describes a dependency or provision.
type Depend struct {
	Name        string
	Version     string
	Description string
	NameHash    uint
	Mod         DepMod
}

// String formats the dependency the way pacman's dep string syntax does.
func (dep Depend) String() string {
	return dep.Name + dep.Mod.String() + dep.Version
}

// File describes an entry in a package's file list.
type File struct {
	Name string
	Size int64
	Mode uint32
}

// BackupFile describes an entry in a package's backup list.
type BackupFile struct {
	Name string
	Hash string
}

// StringList is a list of strings.
type StringList []string

// ForEach calls f for every string in the list.
func (l StringList) ForEach(f func(string) error) error {
	for _, item := range l {
		if err := f(item); err != nil {
			return err
		}
	}

	return nil
}

// Slice returns the list as a plain slice.
func (l StringList) Slice() []string {
	return []string(l)
}

// DependList is a list of dependencies.
type DependList []Depend

// ForEach calls f for every dependency in the list.
func (l DependList) ForEach(f func(*Depend) error) error {
	for i := range l {
		if err := f(&l[i]); err != nil {
			return err
		}
	}

	return nil
}

// Slice returns the list as a plain slice.
func (l DependList) Slice() []Depend {
	return []Depend(l)
}

// BackupList is a list of package backup entries.
type BackupList []BackupFile

// ForEach calls f for every backup entry in the list.
func (l BackupList) ForEach(f func(BackupFile) error) error {
	for _, item := range l {
		if err := f(item); err != nil {
			return err
		}
	}

	return nil
}

// Slice returns the list as a plain slice.
func (l BackupList) Slice() []BackupFile {
	return []BackupFile(l)
}

// ParseDepend parses pacman's dependency syntax: "name", "name=1.0",
// "name>=1.0" and optional dependency descriptions in "name: description".
// It is a Go port of alpm_dep_from_string from pacman 7.1.0's
// lib/libalpm/deps.c (GPL-2.0-or-later).
func ParseDepend(depstring string) Depend {
	dep := Depend{Mod: DepModAny}

	rest := depstring
	if desc := strings.Index(depstring, ": "); desc >= 0 {
		dep.Description = depstring[desc+2:]
		rest = depstring[:desc]
	}

	opIndex := -1
	opLen := 0

	if i := strings.IndexByte(rest, '<'); i >= 0 {
		opIndex = i
		opLen = 1
		dep.Mod = DepModLT
		if i+1 < len(rest) && rest[i+1] == '=' {
			opLen = 2
			dep.Mod = DepModLE
		}
	} else if i := strings.IndexByte(rest, '>'); i >= 0 {
		opIndex = i
		opLen = 1
		dep.Mod = DepModGT
		if i+1 < len(rest) && rest[i+1] == '=' {
			opLen = 2
			dep.Mod = DepModGE
		}
	} else if i := strings.IndexByte(rest, '='); i >= 0 {
		opIndex = i
		opLen = 1
		dep.Mod = DepModEq
	}

	if opIndex < 0 {
		dep.Name = rest
	} else {
		dep.Name = rest[:opIndex]
		dep.Version = rest[opIndex+opLen:]
	}

	dep.NameHash = sdbmHash(dep.Name)

	return dep
}

// MatchLiteral reports whether pkg literally satisfies dep by name and
// version, without considering provisions. It mirrors libalpm's
// _alpm_depcmp_literal.
func MatchLiteral(pkg IPackage, dep Depend) bool {
	return pkg.Name() == dep.Name && depVerCmp(pkg.Version(), dep.Mod, dep.Version)
}

// MatchDependency reports whether pkg satisfies dep, either directly by name
// and version or through one of the package's provisions.
func MatchDependency(pkg IPackage, dep Depend) bool {
	if pkg.Name() == dep.Name && depVerCmp(pkg.Version(), dep.Mod, dep.Version) {
		return true
	}

	for _, provision := range pkg.Provides().Slice() {
		if dep.Mod == DepModAny {
			if provision.Name == dep.Name {
				return true
			}

			continue
		}

		if provision.Mod == DepModEq && provision.Name == dep.Name &&
			depVerCmp(provision.Version, dep.Mod, dep.Version) {
			return true
		}
	}

	return false
}

// depVerCmp checks version1 against version2 using the given constraint.
func depVerCmp(version1 string, mod DepMod, version2 string) bool {
	if mod == DepModAny {
		return true
	}

	cmp := VerCmp(version1, version2)

	switch mod {
	case DepModEq:
		return cmp == 0
	case DepModGE:
		return cmp >= 0
	case DepModLE:
		return cmp <= 0
	case DepModLT:
		return cmp < 0
	case DepModGT:
		return cmp > 0
	default:
		return true
	}
}

// sdbmHash mirrors libalpm's _alpm_hash_sdbm. The hash is only informational
// here, but keeping it makes Depend values behave like go-alpm's.
func sdbmHash(s string) uint {
	var h uint

	for i := 0; i < len(s); i++ {
		h = uint(s[i]) + (h << 6) + (h << 16) - h
	}

	return h
}

package alpm

import (
	"fmt"
	"slices"
)

// PackageList is a list of packages.
type PackageList struct {
	packages []IPackage
}

// NewPackageList builds a PackageList from the given packages.
func NewPackageList(packages []IPackage) PackageList {
	return PackageList{packages: packages}
}

// ForEach calls f for every package in the list.
func (l PackageList) ForEach(f func(IPackage) error) error {
	for _, pkg := range l.packages {
		if err := f(pkg); err != nil {
			return err
		}
	}

	return nil
}

// Slice returns the packages as a plain slice.
func (l PackageList) Slice() []IPackage { return l.packages }

// SortBySize returns a PackageList sorted by installed size, largest first.
// The receiver is left untouched.
func (l PackageList) SortBySize() IPackageList {
	sorted := slices.Clone(l.packages)

	slices.SortStableFunc(sorted, func(a, b IPackage) int {
		switch {
		case a.ISize() > b.ISize():
			return -1
		case a.ISize() < b.ISize():
			return 1
		default:
			return 0
		}
	})

	return NewPackageList(sorted)
}

// FindSatisfier returns the first package that satisfies depstring.
func (l PackageList) FindSatisfier(depstring string) (IPackage, error) {
	dep := ParseDepend(depstring)

	for _, pkg := range l.packages {
		if MatchDependency(pkg, dep) {
			return pkg, nil
		}
	}

	return nil, fmt.Errorf("unable to find dependency %s in PackageList", depstring)
}

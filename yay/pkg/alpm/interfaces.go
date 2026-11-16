package alpm

import "time"

// IPackage is the interface implemented by packages read from pacman's
// databases. It mirrors the method set of go-alpm's IPackage so the embedded
// yay code compiles unchanged.
type IPackage interface {
	FileName() string
	Base() string
	Base64Signature() string
	Validation() Validation
	// Architecture returns the package target Architecture.
	Architecture() string
	// Backup returns a list of package backups.
	Backup() BackupList
	// BuildDate returns the BuildDate of the package.
	BuildDate() time.Time
	// Conflicts returns the conflicts of the package as a DependList.
	Conflicts() IDependList
	// DB returns the package's origin database.
	DB() IDB
	// Depends returns the package's dependency list.
	Depends() IDependList
	// OptionalDepends returns the package's optional dependency list.
	OptionalDepends() IDependList
	// CheckDepends returns the package's check dependency list.
	CheckDepends() IDependList
	// MakeDepends returns the package's make dependency list.
	MakeDepends() IDependList
	// Description returns the package's description.
	Description() string
	// Files returns the file list of the package.
	Files() []File
	// ContainsFile checks if the path is in the package filelist.
	ContainsFile(path string) (File, error)
	// Groups returns the groups the package belongs to.
	Groups() StringList
	// ISize returns the package installed size.
	ISize() int64
	// InstallDate returns the package install date.
	InstallDate() time.Time
	// Licenses returns the package license list.
	Licenses() StringList
	// SHA256Sum returns package SHA256Sum.
	SHA256Sum() string
	// Name returns package name.
	Name() string
	// Packager returns package packager name.
	Packager() string
	// Provides returns DependList of packages provides by package.
	Provides() IDependList
	// Reason returns package install reason.
	Reason() PkgReason
	// Origin returns package origin.
	Origin() PkgFrom
	// Replaces returns a DependList with the packages this package replaces.
	Replaces() IDependList
	// Size returns the packed package size.
	Size() int64
	// URL returns the upstream URL of the package.
	URL() string
	// Version returns the package version.
	Version() string
	// ComputeRequiredBy returns the names of reverse dependencies of a package.
	ComputeRequiredBy() []string
	// ComputeOptionalFor returns the names of packages that optionally
	// require the given package.
	ComputeOptionalFor() []string
	ShouldIgnore() bool
	// SyncNewVersion checks if there is a new version of the
	// package in a given DBlist.
	SyncNewVersion(l IDBList) IPackage
	Type() string
}

// IPackageList is a list of packages.
type IPackageList interface {
	// ForEach executes an action on each package of the PackageList.
	ForEach(func(IPackage) error) error
	// Slice converts the PackageList to a Package Slice.
	Slice() []IPackage
	// SortBySize returns a PackageList sorted by installed size, largest first.
	SortBySize() IPackageList
	// FindSatisfier finds a package that satisfies depstring from PkgList.
	FindSatisfier(string) (IPackage, error)
}

// IDependList is a list of dependencies.
type IDependList interface {
	// ForEach executes an action on each package of the DependList.
	ForEach(func(*Depend) error) error
	// Slice converts the DependList to a Depend Slice.
	Slice() []Depend
}

// IDB is a package database.
type IDB interface {
	Unregister() error
	// Name returns name of the db.
	Name() string
	// Servers returns host server URL.
	Servers() []string
	// SetServers sets server list to use.
	SetServers(servers []string)
	// AddServer adds a string to the server list.
	AddServer(server string)
	// SetUsage sets the Usage of the database.
	SetUsage(usage Usage)
	// Pkg searches a package in db.
	Pkg(name string) IPackage
	// PkgCache returns the list of packages of the database.
	PkgCache() IPackageList
	Search([]string) IPackageList
}

// IDBList is a list of package databases.
type IDBList interface {
	// ForEach executes an action on each DB.
	ForEach(func(IDB) error) error
	// Slice converts DB list to DB slice.
	Slice() []IDB
	// Append modifies DB list with given DB appended.
	Append(IDB)
	// FindGroupPkgs returns a PackageList of packages belonging to a group.
	FindGroupPkgs(string) IPackageList
	// FindSatisfier searches a DBList for a package that satisfies depstring.
	FindSatisfier(string) (IPackage, error)
}

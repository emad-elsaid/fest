// Package alpm provides the pure Go subset of libalpm's data model that the
// embedded yay code needs. It replaces github.com/Jguer/go-alpm/v2, which
// links libalpm through cgo, with plain Go values backed by pacman's on-disk
// databases.
package alpm

// Install reason of a package.
type PkgReason uint

const (
	// PkgReasonExplicit means the package was installed by the user.
	PkgReasonExplicit PkgReason = 0
	// PkgReasonDepend means the package was installed as a dependency.
	PkgReasonDepend PkgReason = 1
)

// String returns a human readable description of the install reason.
func (r PkgReason) String() string {
	switch r {
	case PkgReasonExplicit:
		return "Explicitly installed"
	case PkgReasonDepend:
		return "Installed as a dependency of another package"
	}

	return ""
}

// PkgFrom describes where a package structure came from.
type PkgFrom uint

const (
	// FromFile is a package read from a package archive.
	FromFile PkgFrom = iota + 1
	// FromLocalDB is a package read from the local database.
	FromLocalDB
	// FromSyncDB is a package read from a sync database.
	FromSyncDB
)

// Dependency constraint types.
type DepMod uint

const (
	// DepModAny matches any version.
	DepModAny DepMod = iota + 1
	// DepModEq matches an exact version.
	DepModEq
	// DepModGE matches greater than or equal versions.
	DepModGE
	// DepModLE matches less than or equal versions.
	DepModLE
	// DepModGT matches greater versions.
	DepModGT
	// DepModLT matches lesser versions.
	DepModLT
)

// String returns the operator for a dependency constraint.
func (mod DepMod) String() string {
	switch mod {
	case DepModEq:
		return "="
	case DepModGE:
		return ">="
	case DepModLE:
		return "<="
	case DepModGT:
		return ">"
	case DepModLT:
		return "<"
	}

	return ""
}

// Validation describes which checks a package passed.
type Validation int

const (
	// ValidationNone means no checks were performed.
	ValidationNone Validation = 1 << iota
	// ValidationMD5Sum means an MD5 checksum was validated.
	ValidationMD5Sum
	// ValidationSHA256Sum means a SHA256 checksum was validated.
	ValidationSHA256Sum
	// ValidationSignature means a PGP signature was validated.
	ValidationSignature
	// ValidationUnkown is the zero value.
	ValidationUnkown Validation = 0
)

// Usage is a bitmask of the operations a sync database may be used for.
type Usage int

const (
	// UsageSync allows syncing packages from the database.
	UsageSync Usage = 1 << iota
	// UsageSearch allows searching the database.
	UsageSearch
	// UsageInstall allows installing from the database.
	UsageInstall
	// UsageUpgrade allows upgrading from the database.
	UsageUpgrade
	// UsageAll enables every usage.
	UsageAll = (1 << 4) - 1
)

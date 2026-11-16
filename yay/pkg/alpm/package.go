package alpm

import (
	"errors"
	"sync"
	"time"
)

// errFileNotFound is returned by ContainsFile when a path is not in the
// package's file list, matching go-alpm's behavior.
var errFileNotFound = errors.New("no file")

// PackageData carries the metadata used to build a Package. It exists so the
// database readers can construct packages without exposing every field.
type PackageData struct {
	Name            string
	Version         string
	Base            string
	Description     string
	URL             string
	Architecture    string
	Packager        string
	FileName        string
	SHA256Sum       string
	Base64Signature string
	Validation      Validation
	BuildDate       time.Time
	InstallDate     time.Time
	Size            int64
	ISize           int64
	Reason          PkgReason
	Origin          PkgFrom
	Type            string
	Depends         DependList
	OptionalDepends DependList
	CheckDepends    DependList
	MakeDepends     DependList
	Conflicts       DependList
	Provides        DependList
	Replaces        DependList
	Groups          StringList
	Licenses        StringList
	Backup          BackupList
	DB              IDB
	ShouldIgnore    func() bool
	FilesLoader     func() ([]File, error)
	BackupLoader    func() BackupList
	RequiredBy      func() []string
	OptionalFor     func() []string
}

// Package is a package read from pacman's local or sync databases.
type Package struct {
	name            string
	version         string
	base            string
	description     string
	url             string
	architecture    string
	packager        string
	fileName        string
	sha256Sum       string
	base64Signature string
	validation      Validation
	buildDate       time.Time
	installDate     time.Time
	size            int64
	isize           int64
	reason          PkgReason
	origin          PkgFrom
	pkgType         string
	depends         DependList
	optionalDepends DependList
	checkDepends    DependList
	makeDepends     DependList
	conflicts       DependList
	provides        DependList
	replaces        DependList
	groups          StringList
	licenses        StringList
	backup          BackupList
	db              IDB
	shouldIgnore    func() bool
	requiredBy      func() []string
	optionalFor     func() []string

	filesOnce   sync.Once
	files       []File
	filesLoader func() ([]File, error)

	backupOnce   sync.Once
	loadedBackup BackupList
	backupLoader func() BackupList
}

// NewPackage builds a Package from the given metadata.
func NewPackage(data PackageData) *Package {
	pkgType := data.Type
	if pkgType == "" {
		pkgType = "alpm"
	}

	return &Package{
		name:            data.Name,
		version:         data.Version,
		base:            data.Base,
		description:     data.Description,
		url:             data.URL,
		architecture:    data.Architecture,
		packager:        data.Packager,
		fileName:        data.FileName,
		sha256Sum:       data.SHA256Sum,
		base64Signature: data.Base64Signature,
		validation:      data.Validation,
		buildDate:       data.BuildDate,
		installDate:     data.InstallDate,
		size:            data.Size,
		isize:           data.ISize,
		reason:          data.Reason,
		origin:          data.Origin,
		pkgType:         pkgType,
		depends:         data.Depends,
		optionalDepends: data.OptionalDepends,
		checkDepends:    data.CheckDepends,
		makeDepends:     data.MakeDepends,
		conflicts:       data.Conflicts,
		provides:        data.Provides,
		replaces:        data.Replaces,
		groups:          data.Groups,
		licenses:        data.Licenses,
		backup:          data.Backup,
		db:              data.DB,
		shouldIgnore:    data.ShouldIgnore,
		requiredBy:      data.RequiredBy,
		optionalFor:     data.OptionalFor,
		filesLoader:     data.FilesLoader,
		backupLoader:    data.BackupLoader,
	}
}

// FileName returns the package archive file name.
func (p *Package) FileName() string { return p.fileName }

// Base returns the package base name.
func (p *Package) Base() string { return p.base }

// Base64Signature returns the package's base64 encoded PGP signature.
func (p *Package) Base64Signature() string { return p.base64Signature }

// Validation returns the validation methods applied to the package.
func (p *Package) Validation() Validation { return p.validation }

// Architecture returns the package target architecture.
func (p *Package) Architecture() string { return p.architecture }

// Backup returns a list of package backups. Local database backups are read
// lazily from the package's files entry.
func (p *Package) Backup() BackupList {
	p.backupOnce.Do(func() {
		if p.backupLoader != nil {
			p.loadedBackup = p.backupLoader()
		}
	})

	if p.loadedBackup != nil {
		return p.loadedBackup
	}

	return p.backup
}

// BuildDate returns the build date of the package.
func (p *Package) BuildDate() time.Time { return p.buildDate }

// Conflicts returns the conflicts of the package.
func (p *Package) Conflicts() IDependList { return p.conflicts }

// DB returns the package's origin database.
func (p *Package) DB() IDB { return p.db }

// Depends returns the package's dependency list.
func (p *Package) Depends() IDependList { return p.depends }

// OptionalDepends returns the package's optional dependency list.
func (p *Package) OptionalDepends() IDependList { return p.optionalDepends }

// CheckDepends returns the package's check dependency list.
func (p *Package) CheckDepends() IDependList { return p.checkDepends }

// MakeDepends returns the package's make dependency list.
func (p *Package) MakeDepends() IDependList { return p.makeDepends }

// Description returns the package's description.
func (p *Package) Description() string { return p.description }

// Files returns the file list of the package. Local database files are read
// lazily on first use.
func (p *Package) Files() []File {
	p.filesOnce.Do(p.loadFiles)

	return p.files
}

func (p *Package) loadFiles() {
	if p.filesLoader == nil {
		return
	}

	p.files, _ = p.filesLoader()
}

// ContainsFile checks if the path is in the package filelist.
func (p *Package) ContainsFile(path string) (File, error) {
	for _, file := range p.Files() {
		if file.Name == path {
			return file, nil
		}
	}

	return File{}, errFileNotFound
}

// Groups returns the groups the package belongs to.
func (p *Package) Groups() StringList { return p.groups }

// ISize returns the package installed size.
func (p *Package) ISize() int64 { return p.isize }

// InstallDate returns the package install date.
func (p *Package) InstallDate() time.Time { return p.installDate }

// Licenses returns the package license list.
func (p *Package) Licenses() StringList { return p.licenses }

// SHA256Sum returns the package SHA256 checksum.
func (p *Package) SHA256Sum() string { return p.sha256Sum }

// Name returns the package name.
func (p *Package) Name() string { return p.name }

// Packager returns the package packager name.
func (p *Package) Packager() string { return p.packager }

// Provides returns the list of virtual packages provided by this package.
func (p *Package) Provides() IDependList { return p.provides }

// Reason returns the package install reason.
func (p *Package) Reason() PkgReason { return p.reason }

// Origin returns the package origin.
func (p *Package) Origin() PkgFrom { return p.origin }

// Replaces returns the packages this package replaces.
func (p *Package) Replaces() IDependList { return p.replaces }

// Size returns the packed package size.
func (p *Package) Size() int64 { return p.size }

// URL returns the upstream URL of the package.
func (p *Package) URL() string { return p.url }

// Version returns the package version.
func (p *Package) Version() string { return p.version }

// ComputeRequiredBy returns the names of reverse dependencies of a package.
// The vendored yay code never calls this, so packages built from the on-disk
// databases return nil.
func (p *Package) ComputeRequiredBy() []string {
	if p.requiredBy == nil {
		return nil
	}

	return p.requiredBy()
}

// ComputeOptionalFor returns the names of packages that optionally require
// the given package. The vendored yay code never calls this, so packages
// built from the on-disk databases return nil.
func (p *Package) ComputeOptionalFor() []string {
	if p.optionalFor == nil {
		return nil
	}

	return p.optionalFor()
}

// ShouldIgnore reports whether the package matches IgnorePkg or IgnoreGroup.
func (p *Package) ShouldIgnore() bool {
	if p.shouldIgnore == nil {
		return false
	}

	return p.shouldIgnore()
}

// SyncNewVersion checks if there is a new version of the package in a given
// DB list. Like libalpm's alpm_sync_get_new_version, only the first database
// containing the package name is considered.
func (p *Package) SyncNewVersion(l IDBList) IPackage {
	for _, db := range l.Slice() {
		spkg := db.Pkg(p.name)
		if spkg == nil {
			continue
		}

		if VerCmp(spkg.Version(), p.version) > 0 {
			return spkg
		}

		return nil
	}

	return nil
}

// Type returns the package type.
func (p *Package) Type() string { return p.pkgType }

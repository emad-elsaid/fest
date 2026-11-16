package alpm

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// DB is an in-memory package database backed by pacman's on-disk databases.
// It is immutable after loading; the mutex protects the lazy package cache and
// map reads from concurrent queries.
type DB struct {
	name     string
	servers  []string
	usage    Usage
	packages map[string]IPackage
	order    []string

	cacheMu sync.RWMutex
	cache   []IPackage
	cached  bool
}

// NewDB creates an empty database with the given name.
func NewDB(name string) *DB {
	return &DB{
		name:     name,
		usage:    UsageAll,
		packages: make(map[string]IPackage),
	}
}

// Add inserts or replaces a package in the database.
func (d *DB) Add(pkg IPackage) {
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()

	if _, exists := d.packages[pkg.Name()]; !exists {
		d.order = append(d.order, pkg.Name())
	}

	d.packages[pkg.Name()] = pkg
	d.cached = false
}

// Unregister is a no-op for an in-memory database.
func (d *DB) Unregister() error { return nil }

// Name returns the name of the database.
func (d *DB) Name() string { return d.name }

// Servers returns the configured mirror list.
func (d *DB) Servers() []string { return d.servers }

// SetServers replaces the mirror list.
func (d *DB) SetServers(servers []string) { d.servers = servers }

// AddServer appends a mirror to the mirror list.
func (d *DB) AddServer(server string) { d.servers = append(d.servers, server) }

// SetUsage sets the operations this database may be used for.
func (d *DB) SetUsage(usage Usage) { d.usage = usage }

// Usage returns the operations this database may be used for.
func (d *DB) Usage() Usage { return d.usage }

// Pkg returns the package with the given name, or nil.
func (d *DB) Pkg(name string) IPackage {
	d.cacheMu.RLock()
	defer d.cacheMu.RUnlock()

	return d.packages[name]
}

// PkgCache returns all packages in the database, sorted by name like
// libalpm's package caches.
func (d *DB) PkgCache() IPackageList {
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()

	if d.cached {
		return NewPackageList(d.cache)
	}

	d.cache = make([]IPackage, 0, len(d.order))
	for _, name := range d.order {
		if pkg := d.packages[name]; pkg != nil {
			d.cache = append(d.cache, pkg)
		}
	}

	slices.SortStableFunc(d.cache, func(a, b IPackage) int {
		return cmp.Compare(a.Name(), b.Name())
	})

	d.cached = true

	return NewPackageList(d.cache)
}

// Search returns packages whose name, description, provides or groups match
// every needle. Matching is case-insensitive and treats needles as RE2
// regular expressions, with a plain substring fallback for names. Invalid
// expressions are treated as literals, unlike libalpm which returns no
// results.
func (d *DB) Search(needles []string) IPackageList {
	if d.usage&UsageSearch == 0 || len(needles) == 0 {
		return NewPackageList(nil)
	}

	pkgs := d.PkgCache().Slice()

	for _, needle := range needles {
		if needle == "" {
			continue
		}

		re, err := regexp.Compile("(?i)" + needle)
		if err != nil {
			re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(needle))
		}

		matches := make([]IPackage, 0, len(pkgs))

		for _, pkg := range pkgs {
			if packageMatches(pkg, needle, re) {
				matches = append(matches, pkg)
			}
		}

		pkgs = matches
	}

	return NewPackageList(pkgs)
}

func packageMatches(pkg IPackage, needle string, re *regexp.Regexp) bool {
	if pkg.Name() != "" && (re.MatchString(pkg.Name()) || strings.Contains(pkg.Name(), needle)) {
		return true
	}

	if desc := pkg.Description(); desc != "" && re.MatchString(desc) {
		return true
	}

	for _, provide := range pkg.Provides().Slice() {
		if re.MatchString(provide.Name) {
			return true
		}
	}

	for _, group := range pkg.Groups().Slice() {
		if re.MatchString(group) {
			return true
		}
	}

	return false
}

// DBList is an ordered list of databases.
type DBList struct {
	dbs       []IDB
	local     IDB
	providers ProviderChooser
}

// ProviderChooser selects one of several packages that satisfy a dependency.
type ProviderChooser func(dep Depend, providers []IPackage) IPackage

// NewDBList creates a list containing the given databases.
func NewDBList(dbs ...IDB) *DBList {
	return &DBList{dbs: dbs}
}

// SetLocalDB records the local database used to prefer already-installed
// providers during dependency resolution.
func (l *DBList) SetLocalDB(local IDB) { l.local = local }

// SetProviderChooser installs a callback used when several providers satisfy
// the same dependency.
func (l *DBList) SetProviderChooser(chooser ProviderChooser) { l.providers = chooser }

// ForEach calls f for every database in the list.
func (l *DBList) ForEach(f func(IDB) error) error {
	for _, db := range l.dbs {
		if err := f(db); err != nil {
			return err
		}
	}

	return nil
}

// Slice returns the databases as a plain slice.
func (l *DBList) Slice() []IDB { return l.dbs }

// Append adds a database to the end of the list.
func (l *DBList) Append(db IDB) { l.dbs = append(l.dbs, db) }

// FindGroupPkgs returns every package belonging to the named group. The first
// package with a given name wins, matching libalpm.
func (l *DBList) FindGroupPkgs(name string) IPackageList {
	var pkgs []IPackage

	seen := make(map[string]struct{})

	for _, db := range l.dbs {
		for _, pkg := range db.PkgCache().Slice() {
			if !slices.Contains(pkg.Groups().Slice(), name) {
				continue
			}

			if _, exists := seen[pkg.Name()]; exists {
				continue
			}

			seen[pkg.Name()] = struct{}{}
			pkgs = append(pkgs, pkg)
		}
	}

	return NewPackageList(pkgs)
}

// FindSatisfier returns the first package that satisfies depstring. Literal
// name matches are preferred over providers, matching libalpm.
func (l *DBList) FindSatisfier(depstring string) (IPackage, error) {
	dep := ParseDepend(depstring)

	for _, db := range l.dbs {
		if dbUsage(db)&(UsageInstall|UsageUpgrade) == 0 {
			continue
		}

		if pkg := db.Pkg(dep.Name); pkg != nil && MatchLiteral(pkg, dep) {
			return pkg, nil
		}
	}

	var providers []IPackage

	for _, db := range l.dbs {
		if dbUsage(db)&(UsageInstall|UsageUpgrade) == 0 {
			continue
		}

		for _, pkg := range db.PkgCache().Slice() {
			if pkg.Name() == dep.Name || !MatchDependency(pkg, dep) {
				continue
			}

			if l.local != nil && l.local.Pkg(pkg.Name()) != nil {
				return pkg, nil
			}

			providers = append(providers, pkg)
		}
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("unable to satisfy dependency %s in DBlist", depstring)
	}

	if l.providers != nil {
		if pkg := l.providers(dep, providers); pkg != nil {
			return pkg, nil
		}
	}

	return providers[0], nil
}

// usageDescriber is implemented by databases that expose their usage mask.
type usageDescriber interface {
	Usage() Usage
}

// dbUsage returns a database's usage mask, defaulting to all usages when the
// implementation does not expose one.
func dbUsage(db IDB) Usage {
	if described, ok := db.(usageDescriber); ok {
		return described.Usage()
	}

	return UsageAll
}

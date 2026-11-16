package ialpm

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	pacmanconf "github.com/Morganamilo/go-pacmanconf"

	"github.com/emad-elsaid/fest/yay/pkg/alpm"
	"github.com/emad-elsaid/fest/yay/pkg/db"
	"github.com/emad-elsaid/fest/yay/pkg/settings"
	"github.com/emad-elsaid/fest/yay/pkg/text"
)

// AlpmExecutor implements db.Executor by reading pacman's on-disk databases.
// It replaces go-alpm's cgo handle with a pure Go reader. Package
// transactions are still performed by invoking pacman.
type AlpmExecutor struct {
	conf *pacmanconf.Config
	log  *text.Logger

	localDB      *alpm.DB
	syncDB       *alpm.DBList
	syncIndexes  []syncIndex
	localPkgs    []alpm.IPackage
	localByName  map[string]alpm.IPackage
	sourcesMu    sync.Mutex
	remoteNames  []string
	remotePkgs   map[string]alpm.IPackage
	syncNames    []string
	sourcesReady bool
}

// syncIndex holds per-database lookup structures derived once at load time.
type syncIndex struct {
	db        *alpm.DB
	replacers map[string][]replacerEntry
}

// replacerEntry is a package that replaces a dependency name.
type replacerEntry struct {
	pkg alpm.IPackage
	dep alpm.Depend
}

// NewExecutor loads the local and sync databases described by pacmanConf.
func NewExecutor(pacmanConf *pacmanconf.Config, logger *text.Logger) (*AlpmExecutor, error) {
	if pacmanConf == nil {
		return nil, fmt.Errorf("nil pacman configuration")
	}

	executor := &AlpmExecutor{conf: pacmanConf, log: logger}
	if err := executor.load(); err != nil {
		return nil, err
	}

	return executor, nil
}

// load reads the local and sync databases into memory.
func (ae *AlpmExecutor) load() error {
	dbPath := ae.conf.DBPath
	if dbPath == "" {
		dbPath = "/var/lib/pacman/"
	}

	shouldIgnore := func(name string, groups []string) bool {
		return matchesAny(ae.conf.IgnorePkg, name) || groupsMatchAny(ae.conf.IgnoreGroup, groups)
	}

	localDB, err := loadLocalDB(dbPath, shouldIgnore, ae.log)
	if err != nil {
		return fmt.Errorf("failed to read local database: %w", err)
	}

	syncDB, syncDBs := loadSyncDBs(ae.conf, dbPath, shouldIgnore, ae.log)

	ae.localDB = localDB
	ae.syncDB = syncDB
	ae.syncDB.SetLocalDB(localDB)
	ae.syncDB.SetProviderChooser(ae.chooseProvider)
	ae.syncIndexes = buildSyncIndexes(syncDBs)
	ae.localPkgs = localDB.PkgCache().Slice()

	ae.localByName = make(map[string]alpm.IPackage, len(ae.localPkgs))
	for _, pkg := range ae.localPkgs {
		ae.localByName[pkg.Name()] = pkg
	}

	ae.sourcesMu.Lock()
	ae.remoteNames = nil
	ae.remotePkgs = nil
	ae.syncNames = nil
	ae.sourcesReady = false
	ae.sourcesMu.Unlock()

	return nil
}

// RefreshHandle reloads the databases, for example after `pacman -Sy`. It must
// not be called concurrently with other executor queries; yay calls it before
// starting a transaction.
func (ae *AlpmExecutor) RefreshHandle() error {
	return ae.load()
}

// AlpmArchitectures returns the architectures from pacman.conf.
func (ae *AlpmExecutor) AlpmArchitectures() ([]string, error) {
	return append([]string(nil), ae.conf.Architecture...), nil
}

// LocalSatisfierExists reports whether an installed package satisfies dep.
func (ae *AlpmExecutor) LocalSatisfierExists(pkgName string) bool {
	_, err := ae.localDB.PkgCache().FindSatisfier(pkgName)

	return err == nil
}

// SyncSatisfierExists reports whether a package in the sync databases
// satisfies dep.
func (ae *AlpmExecutor) SyncSatisfierExists(pkgName string) bool {
	_, err := ae.syncDB.FindSatisfier(pkgName)

	return err == nil
}

// IsCorrectVersionInstalled reports whether pkgName is installed at exactly
// versionRequired.
func (ae *AlpmExecutor) IsCorrectVersionInstalled(pkgName, versionRequired string) bool {
	pkg := ae.localDB.Pkg(pkgName)

	return pkg != nil && pkg.Version() == versionRequired
}

// SyncSatisfier returns a sync package that satisfies pkgName.
func (ae *AlpmExecutor) SyncSatisfier(pkgName string) alpm.IPackage {
	pkg, err := ae.syncDB.FindSatisfier(pkgName)
	if err != nil {
		return nil
	}

	return pkg
}

// PackagesFromGroup returns every sync package in the named group.
func (ae *AlpmExecutor) PackagesFromGroup(groupName string) []alpm.IPackage {
	return ae.syncDB.FindGroupPkgs(groupName).Slice()
}

// PackagesFromGroupAndDB returns packages in the named group and database.
func (ae *AlpmExecutor) PackagesFromGroupAndDB(groupName, dbName string) ([]alpm.IPackage, error) {
	syncDB, err := ae.dbByName(dbName)
	if err != nil {
		return nil, err
	}

	var packages []alpm.IPackage

	for _, pkg := range syncDB.PkgCache().Slice() {
		for _, group := range pkg.Groups().Slice() {
			if group == groupName {
				packages = append(packages, pkg)

				break
			}
		}
	}

	return packages, nil
}

// LocalPackages returns every installed package.
func (ae *AlpmExecutor) LocalPackages() []alpm.IPackage {
	return append([]alpm.IPackage(nil), ae.localPkgs...)
}

// SyncPackages returns packages from every sync database. With no arguments
// it returns all packages; otherwise every argument must match.
func (ae *AlpmExecutor) SyncPackages(pkgNames ...string) []alpm.IPackage {
	var packages []alpm.IPackage

	for _, index := range ae.syncIndexes {
		if len(pkgNames) == 0 {
			packages = append(packages, index.db.PkgCache().Slice()...)
		} else {
			packages = append(packages, index.db.Search(pkgNames).Slice()...)
		}
	}

	return packages
}

// LocalPackage returns an installed package by name.
func (ae *AlpmExecutor) LocalPackage(pkgName string) alpm.IPackage {
	return ae.localByName[pkgName]
}

// SyncPackage returns a package by name from the sync databases.
func (ae *AlpmExecutor) SyncPackage(pkgName string) alpm.IPackage {
	for _, index := range ae.syncIndexes {
		if pkg := index.db.Pkg(pkgName); pkg != nil {
			return pkg
		}
	}

	return nil
}

// SyncPackageFromDB returns a package by name from a specific database.
func (ae *AlpmExecutor) SyncPackageFromDB(pkgName, dbName string) alpm.IPackage {
	syncDB, err := ae.dbByName(dbName)
	if err != nil {
		return nil
	}

	return syncDB.Pkg(pkgName)
}

// SatisfierFromDB returns a package from dbName that satisfies pkgName.
func (ae *AlpmExecutor) SatisfierFromDB(pkgName, dbName string) (alpm.IPackage, error) {
	syncDB, err := ae.dbByName(dbName)
	if err != nil {
		return nil, err
	}

	list := alpm.NewDBList(syncDB)
	list.SetLocalDB(ae.localDB)
	list.SetProviderChooser(ae.chooseProvider)

	pkg, err := list.FindSatisfier(pkgName)
	if err != nil {
		return nil, nil //nolint:nilerr // no satisfier is not an error for callers
	}

	return pkg, nil
}

// PackageDepends returns the dependencies of a package.
func (ae *AlpmExecutor) PackageDepends(pkg alpm.IPackage) []alpm.Depend {
	return pkg.Depends().Slice()
}

// PackageOptionalDepends returns the optional dependencies of a package.
func (ae *AlpmExecutor) PackageOptionalDepends(pkg alpm.IPackage) []alpm.Depend {
	return pkg.OptionalDepends().Slice()
}

// PackageProvides returns the provisions of a package.
func (ae *AlpmExecutor) PackageProvides(pkg alpm.IPackage) []alpm.Depend {
	return pkg.Provides().Slice()
}

// PackageGroups returns the groups of a package.
func (ae *AlpmExecutor) PackageGroups(pkg alpm.IPackage) []string {
	return pkg.Groups().Slice()
}

// SyncUpgrades computes the list of repository upgrades, mirroring libalpm's
// alpm_sync_sysupgrade without opening a transaction.
func (ae *AlpmExecutor) SyncUpgrades(enableDowngrade bool) (map[string]db.SyncUpgrade, error) {
	upgrades := make(map[string]db.SyncUpgrade)

	for _, localPkg := range ae.localPkgs {
		// A package already selected as a replacement must not be replaced by a
		// later literal upgrade from another repository.
		if _, selected := upgrades[localPkg.Name()]; selected {
			continue
		}

		for _, index := range ae.syncIndexes {
			syncDB := index.db

			if syncDB.Usage()&alpm.UsageUpgrade == 0 {
				continue
			}

			if ae.addReplacers(upgrades, localPkg, index) {
				break
			}

			syncPkg := syncDB.Pkg(localPkg.Name())
			if syncPkg == nil {
				continue
			}

			ae.addLiteralUpgrade(upgrades, localPkg, syncPkg, enableDowngrade)

			break
		}
	}

	return upgrades, nil
}

// addReplacers adds every non-ignored package in the indexed database that
// replaces localPkg. It reports whether at least one replacer was added.
func (ae *AlpmExecutor) addReplacers(upgrades map[string]db.SyncUpgrade, localPkg alpm.IPackage, index syncIndex) bool {
	added := false

	for _, entry := range index.replacers[localPkg.Name()] {
		if !alpm.MatchLiteral(localPkg, entry.dep) {
			continue
		}

		if entry.pkg.ShouldIgnore() || localPkg.ShouldIgnore() {
			continue
		}

		if existing, ok := upgrades[entry.pkg.Name()]; ok {
			if existing.Package.DB() != index.db {
				continue
			}

			if localPkg.Reason() == alpm.PkgReasonExplicit {
				existing.Reason = alpm.PkgReasonExplicit
				upgrades[entry.pkg.Name()] = existing
			}

			continue
		}

		upgrades[entry.pkg.Name()] = db.SyncUpgrade{
			Package:      entry.pkg,
			LocalVersion: ae.localVersionOrDash(entry.pkg.Name()),
			Reason:       localPkg.Reason(),
		}

		added = true
	}

	return added
}

// addLiteralUpgrade adds a same-name upgrade or downgrade when allowed.
func (ae *AlpmExecutor) addLiteralUpgrade(upgrades map[string]db.SyncUpgrade, localPkg, syncPkg alpm.IPackage, enableDowngrade bool) {
	if syncPkg.ShouldIgnore() || localPkg.ShouldIgnore() {
		return
	}

	cmp := alpm.VerCmp(syncPkg.Version(), localPkg.Version())

	switch {
	case cmp > 0:
		upgrades[syncPkg.Name()] = db.SyncUpgrade{
			Package:      syncPkg,
			LocalVersion: localPkg.Version(),
			Reason:       localPkg.Reason(),
		}
	case cmp < 0 && enableDowngrade:
		upgrades[syncPkg.Name()] = db.SyncUpgrade{
			Package:      syncPkg,
			LocalVersion: localPkg.Version(),
			Reason:       localPkg.Reason(),
		}
	}
}

// BiggestPackages returns installed packages sorted by installed size.
func (ae *AlpmExecutor) BiggestPackages() []alpm.IPackage {
	return ae.localDB.PkgCache().SortBySize().Slice()
}

// LastBuildTime returns the newest build date across the sync databases.
func (ae *AlpmExecutor) LastBuildTime() time.Time {
	var last time.Time

	for _, index := range ae.syncIndexes {
		for _, pkg := range index.db.PkgCache().Slice() {
			if buildDate := pkg.BuildDate(); buildDate.After(last) {
				last = buildDate
			}
		}
	}

	return last
}

// Cleanup releases any resources held by the executor.
func (ae *AlpmExecutor) Cleanup() {}

// Repos returns the configured repository names in order.
func (ae *AlpmExecutor) Repos() []string {
	repos := make([]string, len(ae.syncIndexes))
	for i, index := range ae.syncIndexes {
		repos[i] = index.db.Name()
	}

	return repos
}

// InstalledRemotePackages returns installed packages that are not in any sync
// database, keyed by name.
func (ae *AlpmExecutor) InstalledRemotePackages() map[string]alpm.IPackage {
	ae.computeInstalledBySource()

	return ae.remotePkgs
}

// InstalledRemotePackageNames returns the names of installed packages that are
// not in any sync database.
func (ae *AlpmExecutor) InstalledRemotePackageNames() []string {
	ae.computeInstalledBySource()

	return ae.remoteNames
}

// InstalledSyncPackageNames returns the names of installed packages that are
// also present in a sync database.
func (ae *AlpmExecutor) InstalledSyncPackageNames() []string {
	ae.computeInstalledBySource()

	return ae.syncNames
}

// computeInstalledBySource splits installed packages into repository and
// foreign (AUR) packages.
func (ae *AlpmExecutor) computeInstalledBySource() {
	ae.sourcesMu.Lock()
	defer ae.sourcesMu.Unlock()

	if ae.sourcesReady {
		return
	}

	ae.remotePkgs = make(map[string]alpm.IPackage)
	ae.remoteNames = nil
	ae.syncNames = nil

	for _, localPkg := range ae.localPkgs {
		if ae.SyncPackage(localPkg.Name()) != nil {
			ae.syncNames = append(ae.syncNames, localPkg.Name())
		} else {
			ae.remoteNames = append(ae.remoteNames, localPkg.Name())
			ae.remotePkgs[localPkg.Name()] = localPkg
		}
	}

	ae.sourcesReady = true
}

// SetLogger replaces the executor's logger.
func (ae *AlpmExecutor) SetLogger(logger *text.Logger) {
	ae.log = logger
}

// chooseProvider asks the user to pick among multiple providers, matching
// yay's previous libalpm question callback. With menus hidden or
// --noconfirm it falls back to the first provider.
func (ae *AlpmExecutor) chooseProvider(dep alpm.Depend, providers []alpm.IPackage) alpm.IPackage {
	if len(providers) == 0 {
		return nil
	}

	if len(providers) == 1 || settings.HideMenus || ae.log == nil {
		return providers[0]
	}

	ae.log.OperationInfoln(fmt.Sprintf("There are %d providers available for %s:", len(providers), dep.Name))
	for i, provider := range providers {
		ae.log.OperationInfoln(fmt.Sprintf("%d) %s", i+1, provider.Name()))
	}

	for {
		ae.log.Println("\nEnter a number (default=1): ")

		input, err := ae.log.GetInput("", settings.NoConfirm)
		if err != nil {
			return providers[0]
		}

		input = strings.TrimSpace(input)
		if input == "" {
			return providers[0]
		}

		choice, err := strconv.Atoi(input)
		if err != nil || choice < 1 || choice > len(providers) {
			ae.log.Errorln("invalid value:", input)

			continue
		}

		return providers[choice-1]
	}
}

// localVersionOrDash returns the installed version of name, or "-" when it is
// not installed. yay's version diff printing understands the "-" sentinel for
// packages that are new rather than upgraded.
func (ae *AlpmExecutor) localVersionOrDash(name string) string {
	if pkg := ae.localByName[name]; pkg != nil {
		return pkg.Version()
	}

	return "-"
}

// dbByName returns the sync database with the given name.
func (ae *AlpmExecutor) dbByName(name string) (*alpm.DB, error) {
	for _, index := range ae.syncIndexes {
		if index.db.Name() == name {
			return index.db, nil
		}
	}

	return nil, fmt.Errorf("database not found: %s", name)
}

// buildSyncIndexes derives the per-database lookup tables used by
// SyncUpgrades.
func buildSyncIndexes(syncDBs []*alpm.DB) []syncIndex {
	indexes := make([]syncIndex, len(syncDBs))

	for i, syncDB := range syncDBs {
		index := syncIndex{
			db:        syncDB,
			replacers: make(map[string][]replacerEntry),
		}

		for _, pkg := range syncDB.PkgCache().Slice() {
			for _, replace := range pkg.Replaces().Slice() {
				index.replacers[replace.Name] = append(index.replacers[replace.Name], replacerEntry{
					pkg: pkg,
					dep: replace,
				})
			}
		}

		indexes[i] = index
	}

	return indexes
}

// matchesAny reports whether name matches any of the glob patterns. pacman
// uses fnmatch(3); path.Match is close enough for package and group names, but
// its negation syntax is [^...] instead of [!...], so translate that.
func matchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		pattern = strings.ReplaceAll(pattern, "[!", "[^")

		if matched, err := path.Match(pattern, name); err == nil && matched {
			return true
		}
	}

	return false
}

// groupsMatchAny reports whether any group matches any of the glob patterns.
func groupsMatchAny(patterns, groups []string) bool {
	for _, group := range groups {
		if matchesAny(patterns, group) {
			return true
		}
	}

	return false
}

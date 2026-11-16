//go:build !integration
// +build !integration

package ialpm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pacmanconf "github.com/Morganamilo/go-pacmanconf"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"

	"github.com/emad-elsaid/fest/yay/pkg/alpm"
	"github.com/emad-elsaid/fest/yay/pkg/settings"
	"github.com/emad-elsaid/fest/yay/pkg/text"
)

func TestAlpmExecutor(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	writeLocalPackage(t, dbPath, localPackage{
		name:      "foo",
		version:   "1.0-1",
		depends:   []string{"bar>=2.0"},
		provides:  []string{"foo-virtual=1"},
		groups:    []string{"tools"},
		reason:    "1",
		buildDate: 100,
	})

	writeSyncDB(t, dbPath, "repo1", []syncPackage{
		{name: "foo", version: "2.0-1", depends: []string{"bar>=2.0"}},
		{name: "bar", version: "2.1-1", provides: []string{"bar-api=2"}},
		{name: "baz", version: "1.0-1", groups: []string{"tools"}},
	})

	conf := &pacmanconf.Config{
		RootDir:     "/",
		DBPath:      dbPath,
		IgnorePkg:   []string{"ig*"},
		IgnoreGroup: []string{"ignored"},
		Architecture: []string{
			"x86_64",
		},
		Repos: []pacmanconf.Repository{
			{Name: "repo1", Servers: []string{"https://example.invalid"}, Usage: []string{"All"}},
			{Name: "missing", Servers: []string{"https://example.invalid"}},
		},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	assert.NotNil(t, executor.localDB)
	assert.NotNil(t, executor.syncDB)

	local := executor.LocalPackage("foo")
	require.NotNil(t, local)
	assert.Equal(t, "1.0-1", local.Version())
	assert.Equal(t, alpm.FromLocalDB, local.Origin())
	assert.Equal(t, alpm.PkgReasonDepend, local.Reason())
	assert.Equal(t, time.Unix(100, 0), local.BuildDate())
	assert.Equal(t, []string{"foo-virtual=1"}, dependencyStrings(local.Provides().Slice()))

	arch, err := executor.AlpmArchitectures()
	require.NoError(t, err)
	assert.Equal(t, []string{"x86_64"}, arch)

	assert.Equal(t, []string{"repo1", "missing"}, executor.Repos())

	syncPkg := executor.SyncPackage("foo")
	require.NotNil(t, syncPkg)
	assert.Equal(t, "2.0-1", syncPkg.Version())
	assert.Equal(t, alpm.FromSyncDB, syncPkg.Origin())

	assert.True(t, executor.SyncSatisfierExists("bar-api>=2"))
	assert.Equal(t, "bar", executor.SyncSatisfier("bar-api>=2").Name())
	assert.Equal(t, []string{"baz"}, packageNames(executor.PackagesFromGroup("tools")))

	upgrades, err := executor.SyncUpgrades(false)
	require.NoError(t, err)
	require.Contains(t, upgrades, "foo")
	assert.Equal(t, "1.0-1", upgrades["foo"].LocalVersion)
	assert.Equal(t, "2.0-1", upgrades["foo"].Package.Version())
	assert.Equal(t, alpm.PkgReasonDepend, upgrades["foo"].Reason)
	assert.NotContains(t, upgrades, "bar")

	assert.Equal(t, []string{"foo"}, executor.InstalledSyncPackageNames())
	assert.Empty(t, executor.InstalledRemotePackageNames())

	assert.Equal(t, []string{"foo"}, packageNames(executor.BiggestPackages()))
}

func TestSyncUpgradesIgnoresPackages(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	writeLocalPackage(t, dbPath, localPackage{name: "ignored", version: "1.0-1"})
	writeLocalPackage(t, dbPath, localPackage{name: "grouped", version: "1.0-1", groups: []string{"hold"}})
	writeSyncDB(t, dbPath, "repo1", []syncPackage{
		{name: "ignored", version: "2.0-1"},
		{name: "grouped", version: "2.0-1", groups: []string{"hold"}},
	})

	conf := &pacmanconf.Config{
		DBPath:      dbPath,
		IgnorePkg:   []string{"ign*"},
		IgnoreGroup: []string{"hold"},
		Repos:       []pacmanconf.Repository{{Name: "repo1"}},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	upgrades, err := executor.SyncUpgrades(false)
	require.NoError(t, err)
	assert.Empty(t, upgrades)
}

func TestRefreshHandlePicksUpChanges(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	writeSyncDB(t, dbPath, "repo1", []syncPackage{{name: "foo", version: "1.0-1"}})

	conf := &pacmanconf.Config{
		DBPath: dbPath,
		Repos:  []pacmanconf.Repository{{Name: "repo1"}},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)
	assert.Equal(t, "1.0-1", executor.SyncPackage("foo").Version())

	writeSyncDB(t, dbPath, "repo1", []syncPackage{{name: "foo", version: "2.0-1"}})
	require.NoError(t, executor.RefreshHandle())
	assert.Equal(t, "2.0-1", executor.SyncPackage("foo").Version())
}

func TestSyncUpgradesHandlesReplacers(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	writeLocalPackage(t, dbPath, localPackage{name: "old", version: "1.0-1"})
	writeSyncDB(t, dbPath, "repo1", []syncPackage{
		{name: "new", version: "2.0-1", replaces: []string{"old"}},
	})

	conf := &pacmanconf.Config{
		DBPath: dbPath,
		Repos:  []pacmanconf.Repository{{Name: "repo1"}},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	upgrades, err := executor.SyncUpgrades(false)
	require.NoError(t, err)
	require.Contains(t, upgrades, "new")
	assert.Equal(t, "-", upgrades["new"].LocalVersion)
}

func TestSyncDBMergesSplitMetadata(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()

	writeSyncDBEntries(t, dbPath, "repo1", map[string]string{
		"foo-1.0-1/desc":    "%NAME%\nfoo\n\n%VERSION%\n1.0-1\n\n",
		"foo-1.0-1/depends": "%DEPENDS%\nbar>=1\n\n",
		"foo-1.0-1/files":   "%FILES%\nusr/bin/foo\n\n",
	})

	conf := &pacmanconf.Config{
		DBPath: dbPath,
		Repos:  []pacmanconf.Repository{{Name: "repo1"}},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	pkg := executor.SyncPackage("foo")
	require.NotNil(t, pkg)
	assert.Equal(t, []string{"bar>=1"}, dependencyStrings(pkg.Depends().Slice()))
	require.Len(t, pkg.Files(), 1)
	assert.Equal(t, "usr/bin/foo", pkg.Files()[0].Name)
}

func TestSyncUpgradesDowngrades(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	writeLocalPackage(t, dbPath, localPackage{name: "foo", version: "2.0-1"})
	writeSyncDB(t, dbPath, "repo1", []syncPackage{{name: "foo", version: "1.0-1"}})

	conf := &pacmanconf.Config{
		DBPath: dbPath,
		Repos:  []pacmanconf.Repository{{Name: "repo1"}},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	upgrades, err := executor.SyncUpgrades(false)
	require.NoError(t, err)
	assert.Empty(t, upgrades)

	upgrades, err = executor.SyncUpgrades(true)
	require.NoError(t, err)
	require.Contains(t, upgrades, "foo")
	assert.Equal(t, "2.0-1", upgrades["foo"].LocalVersion)
}

func TestSyncUpgradesSkipsSearchOnlyRepos(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	writeLocalPackage(t, dbPath, localPackage{name: "foo", version: "1.0-1"})
	writeSyncDB(t, dbPath, "repo1", []syncPackage{{name: "foo", version: "2.0-1"}})

	conf := &pacmanconf.Config{
		DBPath: dbPath,
		Repos:  []pacmanconf.Repository{{Name: "repo1", Usage: []string{"Search"}}},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	upgrades, err := executor.SyncUpgrades(false)
	require.NoError(t, err)
	assert.Empty(t, upgrades)
}

func TestSyncUpgradesPromotesReplacerReason(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	writeLocalPackage(t, dbPath, localPackage{name: "aaa", version: "1.0-1", reason: "1"})
	writeLocalPackage(t, dbPath, localPackage{name: "bbb", version: "1.0-1"})
	writeSyncDB(t, dbPath, "repo1", []syncPackage{
		{name: "new", version: "2.0-1", replaces: []string{"aaa", "bbb"}},
	})

	conf := &pacmanconf.Config{
		DBPath: dbPath,
		Repos:  []pacmanconf.Repository{{Name: "repo1"}},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	upgrades, err := executor.SyncUpgrades(false)
	require.NoError(t, err)
	require.Contains(t, upgrades, "new")
	assert.Equal(t, alpm.PkgReasonExplicit, upgrades["new"].Reason)
}

func TestSyncUpgradesKeepsSelectedReplacement(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	writeLocalPackage(t, dbPath, localPackage{name: "aaa", version: "1.0-1"})
	writeLocalPackage(t, dbPath, localPackage{name: "zzz", version: "1.0-1"})
	writeSyncDB(t, dbPath, "core", []syncPackage{{name: "zzz", version: "2.0-1"}})
	writeSyncDB(t, dbPath, "extra", []syncPackage{
		{name: "zzz", version: "3.0-1", replaces: []string{"aaa"}},
	})

	conf := &pacmanconf.Config{
		DBPath: dbPath,
		Repos: []pacmanconf.Repository{
			{Name: "core"},
			{Name: "extra"},
		},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	upgrades, err := executor.SyncUpgrades(false)
	require.NoError(t, err)
	require.Contains(t, upgrades, "zzz")
	assert.Equal(t, "3.0-1", upgrades["zzz"].Package.Version())
}

func TestSatisfierFromDBMissingRepo(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()

	conf := &pacmanconf.Config{DBPath: dbPath}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	_, err = executor.SatisfierFromDB("foo", "nosuchrepo")
	require.Error(t, err)
}

func TestSizeMappings(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	writeLocalDesc(t, dbPath, "foo-1.0-1", "%NAME%\nfoo\n\n%VERSION%\n1.0-1\n\n%SIZE%\n123\n\n")
	writeSyncDBEntries(t, dbPath, "repo1", map[string]string{
		"foo-2.0-1/desc": "%NAME%\nfoo\n\n%VERSION%\n2.0-1\n\n%CSIZE%\n50\n\n%ISIZE%\n200\n\n",
	})

	conf := &pacmanconf.Config{
		DBPath: dbPath,
		Repos:  []pacmanconf.Repository{{Name: "repo1"}},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	local := executor.LocalPackage("foo")
	require.NotNil(t, local)
	assert.Equal(t, int64(0), local.Size())
	assert.Equal(t, int64(123), local.ISize())

	syncPkg := executor.SyncPackage("foo")
	require.NotNil(t, syncPkg)
	assert.Equal(t, int64(50), syncPkg.Size())
	assert.Equal(t, int64(200), syncPkg.ISize())
}

func TestLocalFilesAndBackupsAreLazy(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	pkgDir := filepath.Join(dbPath, "local", "foo-1.0-1")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "desc"), []byte("%NAME%\nfoo\n\n%VERSION%\n1.0-1\n\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "files"), []byte("%FILES%\nusr/bin/foo\n\n%BACKUP%\netc/foo\tHASH\n\n"), 0o644))

	conf := &pacmanconf.Config{DBPath: dbPath}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	pkg := executor.LocalPackage("foo")
	require.NotNil(t, pkg)
	require.Len(t, pkg.Files(), 1)
	assert.Equal(t, "usr/bin/foo", pkg.Files()[0].Name)

	backups := pkg.Backup()
	require.Len(t, backups, 1)
	assert.Equal(t, "etc/foo", backups[0].Name)
	assert.Equal(t, "HASH", backups[0].Hash)
}

func TestCorruptSyncDBDegradesToPartialCache(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	syncDir := filepath.Join(dbPath, "sync")
	require.NoError(t, os.MkdirAll(syncDir, 0o755))

	// Two complete entries, truncated in the middle of the second header. The
	// first package must survive; the second must be dropped.
	var buf bytes.Buffer
	tarWriter := tar.NewWriter(&buf)
	for _, entry := range []struct {
		name    string
		content string
	}{
		{"foo-1.0-1/desc", "%NAME%\nfoo\n\n%VERSION%\n1.0-1\n\n"},
		{"bar-1.0-1/desc", "%NAME%\nbar\n\n%VERSION%\n1.0-1\n\n"},
	} {
		header := &tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.content))}
		require.NoError(t, tarWriter.WriteHeader(header))
		_, err := tarWriter.Write([]byte(entry.content))
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())

	data := buf.Bytes()
	require.NoError(t, os.WriteFile(filepath.Join(syncDir, "repo1.db"), data[:1024+100], 0o644))

	conf := &pacmanconf.Config{
		DBPath: dbPath,
		Repos:  []pacmanconf.Repository{{Name: "repo1"}},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	assert.Equal(t, []string{"repo1"}, executor.Repos())
	require.NotNil(t, executor.SyncPackage("foo"))
	assert.Nil(t, executor.SyncPackage("bar"))
}

func TestSyncDBCompressionFormats(t *testing.T) {
	t.Parallel()

	entries := map[string]string{
		"foo-1.0-1/desc": "%NAME%\nfoo\n\n%VERSION%\n1.0-1\n\n",
	}

	tests := []struct {
		name string
		data func(*testing.T, map[string]string) []byte
	}{
		{"plain", tarPlain},
		{"gzip", tarGz},
		{"xz", tarXz},
		{"zstd", tarZstd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dbPath := t.TempDir()
			syncDir := filepath.Join(dbPath, "sync")
			require.NoError(t, os.MkdirAll(syncDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(syncDir, "repo1.db"), tt.data(t, entries), 0o644))

			conf := &pacmanconf.Config{
				DBPath: dbPath,
				Repos:  []pacmanconf.Repository{{Name: "repo1"}},
			}

			executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
			require.NoError(t, err)
			require.NotNil(t, executor.SyncPackage("foo"))
		})
	}
}

func TestParseUsageValidationAndBackup(t *testing.T) {
	t.Parallel()

	assert.Equal(t, alpm.Usage(15), parseUsage(nil))
	assert.Equal(t, alpm.UsageSearch, parseUsage([]string{"Search"}))
	assert.Equal(t, alpm.UsageSync|alpm.UsageInstall, parseUsage([]string{"Sync", "Install"}))

	assert.Equal(t, alpm.ValidationSHA256Sum|alpm.ValidationSignature,
		parseValidation([]string{"sha256", "pgp"}))
	assert.Equal(t, alpm.ValidationNone, parseValidation([]string{"none"}))

	backups := parseBackup([]string{"etc/foo\tHASH", "etc/bar\tOTHER"})
	require.Len(t, backups, 2)
	assert.Equal(t, "etc/foo", backups[0].Name)
	assert.Equal(t, "HASH", backups[0].Hash)
}

func writeLocalDesc(t *testing.T, dbPath, dir, desc string) {
	t.Helper()

	pkgDir := filepath.Join(dbPath, "local", dir)
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "desc"), []byte(desc), 0o644))
}

func writeSyncDBEntries(t *testing.T, dbPath, repo string, entries map[string]string) {
	t.Helper()

	syncDir := filepath.Join(dbPath, "sync")
	require.NoError(t, os.MkdirAll(syncDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(syncDir, repo+".db"), tarGz(t, entries), 0o644))
}

func tarPlain(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	tarWriter := tar.NewWriter(&buf)

	for name, content := range entries {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}
		require.NoError(t, tarWriter.WriteHeader(header))
		_, err := tarWriter.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, tarWriter.Close())

	return buf.Bytes()
}

func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	_, err := gzipWriter.Write(tarPlain(t, entries))
	require.NoError(t, err)
	require.NoError(t, gzipWriter.Close())

	return buf.Bytes()
}

func tarXz(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	xzWriter, err := xz.NewWriter(&buf)
	require.NoError(t, err)
	_, err = xzWriter.Write(tarPlain(t, entries))
	require.NoError(t, err)
	require.NoError(t, xzWriter.Close())

	return buf.Bytes()
}

func tarZstd(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zstdWriter, err := zstd.NewWriter(&buf)
	require.NoError(t, err)
	_, err = zstdWriter.Write(tarPlain(t, entries))
	require.NoError(t, err)
	require.NoError(t, zstdWriter.Close())

	return buf.Bytes()
}

func TestUnreadableLocalEntryIsSkipped(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir()
	writeLocalPackage(t, dbPath, localPackage{name: "good", version: "1.0-1"})

	brokenDir := filepath.Join(dbPath, "local", "broken-1.0-1")
	require.NoError(t, os.MkdirAll(filepath.Join(brokenDir, "desc"), 0o755))

	conf := &pacmanconf.Config{DBPath: dbPath}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)
	require.NotNil(t, executor.LocalPackage("good"))
	assert.Nil(t, executor.LocalPackage("broken"))
}

func TestExecutorProviderResolution(t *testing.T) {
	oldHideMenus := settings.HideMenus
	settings.HideMenus = true

	t.Cleanup(func() { settings.HideMenus = oldHideMenus })

	dbPath := t.TempDir()
	writeLocalPackage(t, dbPath, localPackage{name: "zzz-provider", version: "1.0-1"})
	writeSyncDB(t, dbPath, "repo1", []syncPackage{
		{name: "aaa-provider", version: "1.0-1", provides: []string{"virtual"}},
		{name: "zzz-provider", version: "1.0-1", provides: []string{"virtual"}},
	})

	conf := &pacmanconf.Config{
		DBPath: dbPath,
		Repos:  []pacmanconf.Repository{{Name: "repo1"}},
	}

	executor, err := NewExecutor(conf, text.NewLogger(io.Discard, io.Discard, strings.NewReader(""), false, "test"))
	require.NoError(t, err)

	require.Equal(t, "zzz-provider", executor.SyncSatisfier("virtual").Name())

	found, err := executor.SatisfierFromDB("virtual", "repo1")
	require.NoError(t, err)
	require.Equal(t, "zzz-provider", found.Name())
}

type localPackage struct {
	name      string
	version   string
	depends   []string
	provides  []string
	replaces  []string
	groups    []string
	reason    string
	buildDate int64
}

type syncPackage struct {
	name     string
	version  string
	depends  []string
	provides []string
	replaces []string
	groups   []string
}

func writeLocalPackage(t *testing.T, dbPath string, pkg localPackage) {
	t.Helper()

	dir := filepath.Join(dbPath, "local", pkg.name+"-"+pkg.version)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "desc"), []byte(descFor(
		pkg.name, pkg.version, pkg.depends, pkg.provides, pkg.replaces, pkg.groups, pkg.reason, pkg.buildDate,
	)), 0o644))
}

func writeSyncDB(t *testing.T, dbPath, repo string, packages []syncPackage) {
	t.Helper()

	entries := make(map[string]string, len(packages))
	for _, pkg := range packages {
		entries[pkg.name+"-"+pkg.version+"/desc"] = descFor(
			pkg.name, pkg.version, pkg.depends, pkg.provides, pkg.replaces, pkg.groups, "", 0,
		)
	}

	writeSyncDBEntries(t, dbPath, repo, entries)
}

func descFor(name, version string, depends, provides, replaces, groups []string, reason string, buildDate int64) string {
	var builder strings.Builder

	writeField := func(key string, values ...string) {
		if len(values) == 0 {
			return
		}

		builder.WriteString("%" + key + "%\n")
		for _, value := range values {
			builder.WriteString(value + "\n")
		}
		builder.WriteString("\n")
	}

	writeField("NAME", name)
	writeField("VERSION", version)
	writeField("DESC", "test package")

	if buildDate > 0 {
		writeField("BUILDDATE", strconv.FormatInt(buildDate, 10))
	}

	if reason != "" {
		writeField("REASON", reason)
	}

	writeField("DEPENDS", depends...)
	writeField("PROVIDES", provides...)
	writeField("REPLACES", replaces...)
	writeField("GROUPS", groups...)

	return builder.String()
}

func dependencyStrings(deps []alpm.Depend) []string {
	result := make([]string, len(deps))
	for i, dep := range deps {
		result[i] = dep.String()
	}

	return result
}

func packageNames(pkgs []alpm.IPackage) []string {
	result := make([]string, len(pkgs))
	for i, pkg := range pkgs {
		result[i] = pkg.Name()
	}

	return result
}

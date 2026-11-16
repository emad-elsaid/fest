package alpm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDBListFindSatisfierPrefersLiteral(t *testing.T) {
	t.Parallel()

	provider := NewPackage(PackageData{
		Name:     "aaa-provider",
		Version:  "1.0-1",
		Provides: DependList{{Name: "foo", Mod: DepModAny}},
	})
	literal := NewPackage(PackageData{Name: "foo", Version: "2.0-1"})

	db := NewDB("repo")
	db.Add(provider)
	db.Add(literal)

	found, err := NewDBList(db).FindSatisfier("foo")
	require.NoError(t, err)
	require.Equal(t, "foo", found.Name())
}

func TestDBListFindSatisfierUsesChooser(t *testing.T) {
	t.Parallel()

	first := NewDB("first")
	first.Add(NewPackage(PackageData{
		Name: "aaa-provider", Version: "1.0-1",
		Provides: DependList{{Name: "virtual", Mod: DepModAny}},
	}))

	second := NewDB("second")
	second.Add(NewPackage(PackageData{
		Name: "zzz-provider", Version: "1.0-1",
		Provides: DependList{{Name: "virtual", Mod: DepModAny}},
	}))

	list := NewDBList(first, second)
	list.SetProviderChooser(func(_ Depend, providers []IPackage) IPackage {
		return providers[len(providers)-1]
	})

	found, err := list.FindSatisfier("virtual")
	require.NoError(t, err)
	require.Equal(t, "zzz-provider", found.Name())
}

func TestDBListFindSatisfierSkipsNonUpgradeDatabases(t *testing.T) {
	t.Parallel()

	db := NewDB("repo")
	db.SetUsage(UsageSearch)
	db.Add(NewPackage(PackageData{Name: "foo", Version: "1.0-1"}))

	list := NewDBList(db)

	_, err := list.FindSatisfier("foo")
	require.Error(t, err)
}

func TestDBListFindSatisfierPrefersInstalledProvider(t *testing.T) {
	t.Parallel()

	first := NewDB("first")
	first.Add(NewPackage(PackageData{
		Name: "aaa-provider", Version: "1.0-1",
		Provides: DependList{{Name: "virtual", Mod: DepModAny}},
	}))

	second := NewDB("second")
	second.Add(NewPackage(PackageData{
		Name: "zzz-provider", Version: "1.0-1",
		Provides: DependList{{Name: "virtual", Mod: DepModAny}},
	}))

	local := NewDB("local")
	local.Add(NewPackage(PackageData{Name: "zzz-provider", Version: "1.0-1"}))

	list := NewDBList(first, second)
	list.SetLocalDB(local)

	found, err := list.FindSatisfier("virtual")
	require.NoError(t, err)
	require.Equal(t, "zzz-provider", found.Name())
}

func TestDBSearchRespectsUsage(t *testing.T) {
	t.Parallel()

	db := NewDB("repo")
	db.Add(NewPackage(PackageData{Name: "foo", Version: "1.0-1"}))

	db.SetUsage(UsageInstall)
	require.Empty(t, db.Search([]string{"foo"}).Slice())

	db.SetUsage(UsageSearch)
	require.Len(t, db.Search([]string{"foo"}).Slice(), 1)
}

func TestDBSearchEmptyNeedles(t *testing.T) {
	t.Parallel()

	db := NewDB("repo")
	db.Add(NewPackage(PackageData{Name: "foo", Version: "1.0-1"}))

	require.Empty(t, db.Search(nil).Slice())
	require.Empty(t, db.Search([]string{}).Slice())
}

func TestDBSearchRegexAndFallback(t *testing.T) {
	t.Parallel()

	db := NewDB("repo")
	db.Add(NewPackage(PackageData{Name: "foo-bar", Version: "1.0-1", Description: "a tool"}))
	db.Add(NewPackage(PackageData{Name: "baz", Version: "1.0-1"}))
	db.Add(NewPackage(PackageData{Name: "foo-[", Version: "1.0-1"}))

	require.Len(t, db.Search([]string{"^foo-"}).Slice(), 2)

	// An invalid RE2 expression falls back to a literal match.
	require.Len(t, db.Search([]string{"foo-["}).Slice(), 1)
	require.Equal(t, "foo-[", db.Search([]string{"foo-["}).Slice()[0].Name())
}

func TestPkgCacheIsSortedByName(t *testing.T) {
	t.Parallel()

	db := NewDB("repo")
	db.Add(NewPackage(PackageData{Name: "zebra", Version: "1.0-1"}))
	db.Add(NewPackage(PackageData{Name: "alpha", Version: "1.0-1"}))

	pkgs := db.PkgCache().Slice()
	require.Len(t, pkgs, 2)
	require.Equal(t, "alpha", pkgs[0].Name())
	require.Equal(t, "zebra", pkgs[1].Name())
}

func TestFindGroupPkgsDeduplicates(t *testing.T) {
	t.Parallel()

	first := NewDB("first")
	first.Add(NewPackage(PackageData{Name: "foo", Version: "1.0-1", Groups: StringList{"tools"}, DB: first}))

	second := NewDB("second")
	second.Add(NewPackage(PackageData{Name: "foo", Version: "2.0-1", Groups: StringList{"tools"}, DB: second}))

	pkgs := NewDBList(first, second).FindGroupPkgs("tools").Slice()
	require.Len(t, pkgs, 1)
	require.Equal(t, "first", pkgs[0].DB().Name())
}

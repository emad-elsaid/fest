package alpm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDepend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		name    string
		version string
		mod     DepMod
		desc    string
	}{
		{"glibc", "glibc", "", DepModAny, ""},
		{"glibc=2.39", "glibc", "2.39", DepModEq, ""},
		{"glibc>=2.39", "glibc", "2.39", DepModGE, ""},
		{"glibc<=2.39", "glibc", "2.39", DepModLE, ""},
		{"glibc>2.39", "glibc", "2.39", DepModGT, ""},
		{"glibc<2.39", "glibc", "2.39", DepModLT, ""},
		{"foo>=1.0: optional helper", "foo", "1.0", DepModGE, "optional helper"},
		{"foo:1.0", "foo:1.0", "", DepModAny, ""},
		{"libfoo.so=1-64", "libfoo.so", "1-64", DepModEq, ""},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			dep := ParseDepend(tt.in)
			require.Equal(t, tt.name, dep.Name)
			require.Equal(t, tt.version, dep.Version)
			require.Equal(t, tt.mod, dep.Mod)
			require.Equal(t, tt.desc, dep.Description)
		})
	}
}

func TestMatchDependency(t *testing.T) {
	t.Parallel()

	pkg := NewPackage(PackageData{
		Name:    "foo",
		Version: "2.0-1",
		Provides: DependList{
			{Name: "libfoo.so", Version: "1-64", Mod: DepModEq},
			{Name: "foo-virtual", Mod: DepModAny},
		},
	})

	tests := []struct {
		dep  string
		want bool
	}{
		{"foo", true},
		{"foo>=2.0", true},
		{"foo>2.0", false},
		{"foo=2.0-1", true},
		{"bar", false},
		{"libfoo.so", true},
		{"libfoo.so=1-64", true},
		{"libfoo.so>=2", false},
		{"foo-virtual", true},
		{"foo-virtual>=1", false},
	}

	for _, tt := range tests {
		t.Run(tt.dep, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, MatchDependency(pkg, ParseDepend(tt.dep)))
		})
	}
}

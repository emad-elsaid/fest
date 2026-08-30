package fest

import (
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

// socketManager returns a systemdManager built with the given unit type and
// optional socket list, used to exercise ImplicitWanted behaviour.
func socketManager(unitType string, user bool, socketWanted *[]string) *systemdManager {
	mgr := newSystemd(
		ResourceName("test "+unitType), unitType, "F", "f.go", unitType, false, &[]string{},
	)
	mgr.user = user
	mgr.socketWanted = socketWanted
	return mgr
}

func TestImplicitWanted_NonServiceManager(t *testing.T) {
	sockets := &[]string{"pipewire"}
	tests := []struct {
		name     string
		unitType string
	}{
		{name: "timers do not activate services", unitType: "timer"},
		{name: "sockets do not activate services", unitType: "socket"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr := socketManager(tc.unitType, false, sockets)
			require.Nil(t, mgr.ImplicitWanted(),
				"non-service managers must not report implicitly wanted services")
		})
	}
}

func TestImplicitWanted_NoSocketWanted(t *testing.T) {
	mgr := socketManager("service", false, nil)
	require.Nil(t, mgr.ImplicitWanted(),
		"service manager without a socket list must not report implicit services")
}

func TestImplicitWanted_EmptySocketWanted(t *testing.T) {
	mgr := socketManager("service", false, &[]string{})
	require.Empty(t, mgr.ImplicitWanted(),
		"service manager with no declared sockets has no implicit services")
}

func TestSocketServicesByTriggers(t *testing.T) {
	tests := []struct {
		name     string
		sockets  []string
		triggers map[string][]string
		want     []string
	}{
		{
			name:    "same-name default when systemd reports no triggers",
			sockets: []string{"pipewire", "docker"},
			want:    []string{"pipewire", "docker"},
		},
		{
			name:     "explicit trigger overrides same-name",
			sockets:  []string{"foo"},
			triggers: map[string][]string{"foo": {"bar"}},
			want:     []string{"bar"},
		},
		{
			name:     "mixed override and fallback",
			sockets:  []string{"foo", "baz"},
			triggers: map[string][]string{"foo": {"bar"}},
			want:     []string{"bar", "baz"},
		},
		{
			name:     "duplicate triggers are de-duplicated",
			sockets:  []string{"foo", "qux"},
			triggers: map[string][]string{"foo": {"bar"}, "qux": {"bar"}},
			want:     []string{"bar"},
		},
		{
			name:    "no sockets yields no services",
			sockets: nil,
			want:    []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := socketServicesByTriggers(tc.sockets, tc.triggers)
			require.ElementsMatch(t, tc.want, got,
				"socket services should resolve to %v, got %v", tc.want, got)
			require.Equal(t, len(lo.Uniq(tc.want)), len(got),
				"result should not contain duplicates")
		})
	}
}

// unwantedManager is a minimal packageManager used to exercise
// unwantedResources without touching a real systemd instance.
type unwantedManager struct{}

func (unwantedManager) ResourceName() string             { return "test" }
func (unwantedManager) Wanted() []string                 { return nil }
func (unwantedManager) Match(want, have string) bool     { return want == have }
func (unwantedManager) ListInstalled() ([]string, error) { return nil, nil }
func (unwantedManager) ListExplicit() ([]string, error)  { return nil, nil }
func (unwantedManager) Install([]string) error           { return nil }
func (unwantedManager) Uninstall([]string) error         { return nil }
func (unwantedManager) MarkExplicit([]string) error      { return nil }
func (unwantedManager) Update() error                    { return nil }
func (unwantedManager) GetDependencies() (map[string][]string, error) {
	return nil, nil
}
func (unwantedManager) SaveAsGo([]string) error { return nil }

func TestUnwantedResources_PreservesImplicit(t *testing.T) {
	tests := []struct {
		name      string
		installed []string
		wanted    []string
		implicit  []string
		keep      map[string]bool
		want      []string
	}{
		{
			name:      "service activated by declared socket is preserved",
			installed: []string{"pipewire", "docker"},
			implicit:  []string{"pipewire"},
			want:      []string{"docker"},
		},
		{
			name:      "without implicit the service is considered unwanted",
			installed: []string{"pipewire"},
			want:      []string{"pipewire"},
		},
		{
			name:      "explicit wanted beats implicit",
			installed: []string{"pipewire", "docker"},
			wanted:    []string{"docker"},
			implicit:  []string{"pipewire"},
			want:      []string{},
		},
		{
			name:      "kept dependency wins over implicit",
			installed: []string{"pipewire"},
			implicit:  []string{"pipewire"},
			keep:      map[string]bool{"pipewire": true},
			want:      []string{},
		},
		{
			name:      "empty installed yields no uninstall",
			installed: []string{},
			want:      []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var pm unwantedManager
			got := unwantedResources(pm, tc.installed, tc.wanted, tc.implicit, tc.keep)
			require.ElementsMatch(t, tc.want, got,
				"unwanted resources should be %v, got %v", tc.want, got)
		})
	}
}

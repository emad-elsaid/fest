package fest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ============================================================
// Bug 1 & 2: Race conditions in callback registration
// ============================================================

func TestBefore_ConcurrentRegistration_RaceCondition(t *testing.T) {
	// SETUP: Reset global state
	callbacks.before = make(map[string][]Callback)
	callbacks.after = make(map[string][]Callback)

	// BUG: Before() modifies global map without synchronization
	// This test fails with `go test -race`
	const testResource ResourceName = "test-resource"
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Before(testResource, func() {})
		}()
	}
	wg.Wait()

	// If no race, should have 100 callbacks registered
	require.Len(t, callbacks.before[string(testResource)], 100,
		"Expected 100 callbacks but race condition may cause lost writes")
}

func TestAfter_ConcurrentRegistration_RaceCondition(t *testing.T) {
	// SETUP: Reset global state
	callbacks.before = make(map[string][]Callback)
	callbacks.after = make(map[string][]Callback)

	// BUG: After() modifies global map without synchronization
	// This test fails with `go test -race`
	const testResource ResourceName = "test-resource"
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			After(testResource, func() {})
		}()
	}
	wg.Wait()

	// If no race, should have 100 callbacks registered
	require.Len(t, callbacks.after[string(testResource)], 100,
		"Expected 100 callbacks but race condition may cause lost writes")
}

func TestOnCommand_ConcurrentRegistration_RaceCondition(t *testing.T) {
	// SETUP: Reset global state
	commandCallbacks = make(map[CommandPhase][]Callback)

	// BUG: OnCommand() modifies global map without synchronization
	// This test fails with `go test -race`
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			OnCommand(PhaseBeforeApply, func() {})
		}()
	}
	wg.Wait()

	// If no race, should have 100 callbacks registered
	require.Len(t, commandCallbacks[PhaseBeforeApply], 100,
		"Expected 100 callbacks but race condition may cause lost writes")
}

// ============================================================
// Bug 3: executeCallbacks lacks panic recovery
// ============================================================

func TestExecuteCallbacks_PanicingCallback_CrashesProgram(t *testing.T) {
	// SETUP: Create a callback map with a panicking callback
	callbackMap := map[string][]Callback{
		"test-resource": {
			func() { /* normal callback */ },
			func() { panic("callback panic") }, // This should be recovered
			func() { /* this should run */ },
		},
	}

	// FIXED: executeCallbacks now recovers from panics
	// Unlike the bug where it would crash
	require.NotPanics(t, func() {
		executeCallbacks("test-resource", callbackMap, "before")
	}, "executeCallbacks should recover from panics and not crash")
}

func TestExecuteCallbacks_PanicPreventsSubsequentCallbacks(t *testing.T) {
	callbackMap := map[string][]Callback{
		"test-resource": {},
	}

	executed := []int{}
	callbackMap["test-resource"] = []Callback{
		func() { executed = append(executed, 1) },
		func() { panic("oops") },
		func() { executed = append(executed, 3) }, // Now runs due to panic recovery
	}

	// FIXED: Panic should be recovered, allowing subsequent callbacks
	require.NotPanics(t, func() {
		executeCallbacks("test-resource", callbackMap, "before")
	}, "executeCallbacks should recover from panics")

	// FIXED: All callbacks should execute despite panic in callback 2
	require.Equal(t, []int{1, 3}, executed,
		"All callbacks should execute, panic is recovered")
}

// ============================================================
// Bug 4: syncPackages silently ignores error from ListInstalled
// ============================================================

type mockPackageManagerWithFailingRefresh struct {
	installed     []string
	explicit      []string
	failOnSecond  bool
	listCallCount int
}

func (m *mockPackageManagerWithFailingRefresh) ResourceName() string { return "mock" }
func (m *mockPackageManagerWithFailingRefresh) Wanted() []string     { return []string{} }
func (m *mockPackageManagerWithFailingRefresh) Match(w, h string) bool {
	return w == h
}

func (m *mockPackageManagerWithFailingRefresh) ListInstalled() ([]string, error) {
	m.listCallCount++
	if m.failOnSecond && m.listCallCount == 2 {
		return nil, errors.New("mock error") // BUG: This error was silently ignored by syncPackages
	}
	return m.installed, nil
}

func (m *mockPackageManagerWithFailingRefresh) ListExplicit() ([]string, error) {
	return m.explicit, nil
}

func (m *mockPackageManagerWithFailingRefresh) Install(pkgs []string) error {
	m.installed = append(m.installed, pkgs...)
	return nil
}

func (m *mockPackageManagerWithFailingRefresh) Uninstall(pkgs []string) error { return nil }
func (m *mockPackageManagerWithFailingRefresh) MarkExplicit(pkgs []string) error {
	return nil
}
func (m *mockPackageManagerWithFailingRefresh) Update() error { return nil }
func (m *mockPackageManagerWithFailingRefresh) GetDependencies() (map[string][]string, error) {
	return nil, nil
}
func (m *mockPackageManagerWithFailingRefresh) SaveAsGo(wanted []string) error { return nil }

func TestSyncPackages_ListInstalledErrorAfterInstall_SilentlyIgnored(t *testing.T) {
	// SETUP: Mock that fails on second ListInstalled call
	mock := &mockPackageManagerWithFailingRefresh{
		installed:    []string{},
		explicit:     []string{},
		failOnSecond: true,
	}

	wanted := []string{"new-package"}

	// FIXED: syncPackages now properly handles error from ListInstalled
	// Previously: `installed, _ = pm.ListInstalled()`
	// Now: `installed, err = pm.ListInstalled()` with proper error handling
	err := syncPackages(mock, wanted)

	// This test now fails correctly - the error is properly returned
	require.Error(t, err, "syncPackages should return error from ListInstalled refresh")
	require.Equal(t, "mock error", err.Error())

	// Verify the second call actually failed
	require.Equal(t, 2, mock.listCallCount,
		"ListInstalled should be called twice (initial + refresh after install)")
}

// Registering hooks via Before/After is guarded by the callbacks mutex, but
// executeCallbacks reads callbacks.before / callbacks.after directly, with no
// synchronization. A hook registered from any goroutine
// other than the apply/update goroutine therefore races the map read.
//
// Correct behavior: executeCallbacks must synchronize with the same registry
// mutex. This test holds the write lock and invokes executeCallbacks from
// another goroutine; a correctly synchronized implementation blocks until the
// lock is released, whereas the current implementation returns immediately.
func TestInterfaceBugExecuteCallbacksReadsRegistryWithoutLock(t *testing.T) {
	callbacks.Lock()

	done := make(chan struct{})
	go func() {
		executeCallbacks("interface-bug-sync-check", callbacks.before, "before")
		close(done)
	}()

	select {
	case <-done:
		callbacks.Unlock()
		t.Fatal("executeCallbacks read callbacks.before without acquiring the registry lock; " +
			"concurrent Before/After registration is a data race")
	case <-time.After(200 * time.Millisecond):
		callbacks.Unlock()
	}
}

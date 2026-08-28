package fest

import (
	"strings"

	"github.com/emad-elsaid/types"
	"github.com/samber/lo"
)

const (
	ResourceSystemServices ResourceName = "system services"
	ResourceSystemTimers   ResourceName = "system timers"
	ResourceSystemSockets  ResourceName = "system sockets"
	ResourceUserServices   ResourceName = "user services"
	ResourceUserTimers     ResourceName = "user timers"
	ResourceUserSockets    ResourceName = "user sockets"
)

var (
	systemServices []string
	systemTimers   []string
	systemSockets  []string
	services       []string
	timers         []string
	sockets        []string
)

// SystemService declares system-level systemd services to enable.
// Services are enabled and started on apply.
//
// Example:
//
//	fest.SystemService("docker", "sshd")
func SystemService(svcs ...string) { addUnique(&systemServices, svcs...) }

// SystemTimer declares system-level systemd timers to enable.
//
// Example:
//
//	fest.SystemTimer("fstrim")
func SystemTimer(tmrs ...string) { addUnique(&systemTimers, tmrs...) }

// SystemSocket declares system-level systemd sockets to enable.
//
// Example:
//
//	fest.SystemSocket("docker")
func SystemSocket(socks ...string) { addUnique(&systemSockets, socks...) }

// Service declares user-level systemd services to enable.
// Services run as the current user.
//
// Example:
//
//	fest.Service("syncthing", "ssh-agent")
func Service(svcs ...string) { addUnique(&services, svcs...) }

// Timer declares user-level systemd timers to enable.
//
// Example:
//
//	fest.Timer("backup")
func Timer(tmrs ...string) { addUnique(&timers, tmrs...) }

// Socket declares user-level systemd sockets to enable.
//
// Example:
//
//	fest.Socket("pipewire")
func Socket(socks ...string) { addUnique(&sockets, socks...) }

type systemdManager struct {
	resource        ResourceName
	unitType        string
	user            bool
	wanted          *[]string
	funcName        string
	filename        string
	successMsg      string
	cachedInstalled []string
	cached          bool
	socketWanted    *[]string
}

func (s systemdManager) ResourceName() string         { return string(s.resource) }
func (s systemdManager) Wanted() []string             { return *s.wanted }
func (s systemdManager) Match(want, have string) bool { return want == have }

func (s *systemdManager) ListInstalled() ([]string, error) {
	if s.cached {
		return s.cachedInstalled, nil
	}
	units, err := listSystemdUnits(s.unitType, s.user)
	if err == nil {
		s.cachedInstalled = units
		s.cached = true
	}
	return units, err
}

func (s *systemdManager) ListExplicit() ([]string, error) { return s.ListInstalled() }

func (s *systemdManager) Install(units []string) error {
	var args []string
	if s.user {
		args = append(args, "--user")
	}

	args = append(args, "enable", "--now")
	for _, unit := range units {
		args = append(args, unit+"."+s.unitType)
	}

	if s.user {
		return types.Cmd("systemctl", args...).Interactive().Error()
	}

	return types.Sudo("systemctl", args...).Interactive().Error()
}

func (s *systemdManager) Uninstall(units []string) error {
	var args []string
	if s.user {
		args = append(args, "--user")
	}

	args = append(args, "disable", "--now")
	for _, unit := range units {
		args = append(args, unit+"."+s.unitType)
	}

	if s.user {
		return types.Cmd("systemctl", args...).Interactive().Error()
	}

	return types.Sudo("systemctl", args...).Interactive().Error()
}

func (s *systemdManager) MarkExplicit([]string) error                   { return nil }
func (s *systemdManager) GetDependencies() (map[string][]string, error) { return nil, nil }

// ImplicitWanted returns service units that are already present because a
// declared socket activates them (socket activation). These are implicitly
// wanted: fest must not enable, disable, or persist them as a separate
// resource. Only meaningful for service managers; other unit types return nil.
func (s *systemdManager) ImplicitWanted() []string {
	if s.unitType != "service" || s.socketWanted == nil {
		return nil
	}
	return socketActivatedServices(*s.socketWanted, s.user)
}

func (s *systemdManager) SaveAsGo(wanted []string) error {
	installed, err := s.ListInstalled()
	if err != nil {
		return err
	}

	diff := lo.Without(installed, wanted...)
	diff = lo.Without(diff, s.ImplicitWanted()...)
	if len(diff) == 0 {
		logSuccess("No new " + s.successMsg + " to save")
		return nil
	}

	if err := saveAsGoFile(s.filename, s.funcName, diff); err != nil {
		return err
	}
	logSuccess(s.successMsg+" saved", "file", s.filename, "count", len(diff))
	return nil
}

// socketActivatedServices returns the service units that are activated by the
// given sockets. Each socket triggers its same-name service by default, or an
// explicitly configured service via the socket's Service= option. These become
// implicitly present so fest does not manage them as separate resources.
func socketActivatedServices(sockets []string, user bool) []string {
	triggers := make(map[string][]string, len(sockets))
	for _, sock := range sockets {
		triggers[sock] = socketTriggerServices(sock, user)
	}
	return socketServicesByTriggers(sockets, triggers)
}

// socketTriggerServices returns the bare names of the services a socket
// activates according to systemd's Triggers property. Empty when the socket is
// unknown or does not trigger a service.
func socketTriggerServices(sock string, user bool) []string {
	args := []string{"show", sock + ".socket", "--property=Triggers", "--value"}
	if user {
		args = append([]string{"--user"}, args...)
	}
	stdout, err := types.Cmd("systemctl", args...).StdoutErr()
	if err != nil || strings.TrimSpace(stdout) == "" {
		return nil
	}
	return lo.FilterMap(strings.Fields(stdout), func(unit string, _ int) (string, bool) {
		unit = strings.TrimSuffix(unit, ".service")
		return unit, unit != ""
	})
}

// socketServicesByTriggers resolves the service activated by each socket using
// an explicit trigger map, falling back to the same-name service when a socket
// activates none.
func socketServicesByTriggers(sockets []string, triggers map[string][]string) []string {
	services := make([]string, 0, len(sockets))
	for _, sock := range sockets {
		next := triggers[sock]
		if len(next) == 0 {
			next = []string{sock}
		}
		services = append(services, next...)
	}
	return lo.Uniq(services)
}

// Package stow implements GNU Stow-style symlink farm management in pure Go.
//
// Given a stow directory containing package subdirectories and a target
// directory (such as $HOME), it creates relative symlinks in the target
// directory pointing back to the package's files, folding entire directories
// into a single symlink when possible and unfolding them when multiple packages
// or existing target directories share a path.
//
// The planner layers planned operations over the real filesystem, so a plan
// always reflects the state that will exist once earlier actions are executed.
package stow

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// Options configures a stow or unstow operation.
type Options struct {
	// StowDir is the directory containing stow packages (equivalent to -d).
	// Defaults to "." if empty.
	StowDir string

	// TargetDir is the directory where symlinks are created (equivalent to -t).
	// Defaults to the parent of StowDir if empty.
	TargetDir string

	// Simulate plans operations without modifying the filesystem (-n/--simulate).
	Simulate bool

	// Adopt moves existing plain target files into the stow package and
	// replaces them with symlinks (--adopt).
	Adopt bool

	// Override lists regular expressions matching relative target paths whose
	// existing symlinks may be replaced (--override). As in GNU Stow, this only
	// applies to links owned by a stow package.
	Override []*regexp.Regexp

	// Skip lists relative target paths to leave untouched.
	Skip map[string]bool

	// Verbose writes planned or executed operations to Writer.
	Verbose bool

	// Writer receives verbose/simulation output. Defaults to os.Stdout when
	// Verbose is true.
	Writer io.Writer
}

// ActionKind identifies a filesystem operation planned or performed by stow.
type ActionKind string

const (
	ActionLink   ActionKind = "LINK"
	ActionRemove ActionKind = "UNLINK"
	ActionAdopt  ActionKind = "ADOPT"
	ActionUnfold ActionKind = "UNFOLD"
)

// Action represents a single planned or executed stow operation.
type Action struct {
	Kind   ActionKind
	Target string // Path in TargetDir
	Source string // Path in StowDir (or link destination)
}

// String formats an Action in a human-readable GNU Stow-style line.
func (a Action) String() string {
	switch a.Kind {
	case ActionLink:
		return fmt.Sprintf("LINK: %s => %s", a.Target, a.Source)
	case ActionRemove:
		return fmt.Sprintf("UNLINK: %s", a.Target)
	case ActionAdopt:
		return fmt.Sprintf("ADOPT: %s -> %s", a.Target, a.Source)
	case ActionUnfold:
		return fmt.Sprintf("UNFOLD: %s", a.Target)
	default:
		return fmt.Sprintf("%s: %s", a.Kind, a.Target)
	}
}

// Conflict describes a target path that blocks stowing without user action
// or --adopt / --override.
type Conflict struct {
	RelPath    string
	TargetPath string
	SourcePath string
	Reason     string
}

// ConflictError is returned when one or more conflicts prevent stowing.
type ConflictError struct {
	Package   string
	Conflicts []Conflict
}

func (e *ConflictError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "WARNING! stowing %s would cause conflicts:\n", e.Package)
	for _, c := range e.Conflicts {
		fmt.Fprintf(&b, "  * %s\n", c.Reason)
	}
	b.WriteString("All operations aborted.")
	return b.String()
}

// Plan holds the actions and conflicts computed for a stow operation.
type Plan struct {
	Actions   []Action
	Conflicts []Conflict
}

// localIgnoreFile is ignored at the root of every package, and its presence
// replaces the built-in ignore list for that package.
const localIgnoreFile = ".stow-local-ignore"

// globalIgnoreFile is consulted when the package has no local ignore file.
const globalIgnoreFile = ".stow-global-ignore"

// builtinIgnorePatterns mirrors GNU Stow's built-in ignore list.
var builtinIgnorePatterns = []string{
	`RCS`,
	`.+,v`,
	`CVS`,
	`\.\#.+`,
	`\.cvsignore`,
	`\.svn`,
	`_darcs`,
	`\.hg`,
	`\.git`,
	`\.gitignore`,
	`\.gitmodules`,
	`.+~`,
	`\#.*\#`,
	`^/README.*`,
	`^/LICENSE.*`,
	`^/COPYING`,
}

// ignoreMatcher applies GNU Stow ignore rules: patterns without a slash match a
// path segment at any depth, patterns with a slash match the whole relative
// path, and the package's .stow-local-ignore (or $HOME/.stow-global-ignore)
// replaces the built-in list entirely.
type ignoreMatcher struct {
	segment *regexp.Regexp
	path    *regexp.Regexp
}

func newIgnoreMatcher(patterns []string) (*ignoreMatcher, error) {
	patterns = append(patterns, `^/\.stow-local-ignore$`)

	var segments, paths []string
	for _, p := range patterns {
		if strings.Contains(p, "/") {
			paths = append(paths, p)
		} else {
			segments = append(segments, p)
		}
	}

	m := &ignoreMatcher{}
	var err error
	if len(segments) > 0 {
		m.segment, err = regexp.Compile("^(" + strings.Join(segments, "|") + ")$")
		if err != nil {
			return nil, fmt.Errorf("invalid stow ignore pattern: %w", err)
		}
	}
	if len(paths) > 0 {
		m.path, err = regexp.Compile("(^|/)(" + strings.Join(paths, "|") + ")(/|$)")
		if err != nil {
			return nil, fmt.Errorf("invalid stow ignore pattern: %w", err)
		}
	}
	return m, nil
}

// isIgnored reports whether the package-relative path relPath is ignored.
func (m *ignoreMatcher) isIgnored(relPath string) bool {
	slashPath := filepath.ToSlash(relPath)
	if m.segment != nil && m.segment.MatchString(path.Base(slashPath)) {
		return true
	}
	return m.path != nil && m.path.MatchString("/"+slashPath)
}

// loadIgnoreMatcher builds the ignore rules for a package, preferring the
// package's local ignore file over the user's global one and then the built-in
// list, exactly like GNU Stow.
func loadIgnoreMatcher(stowDir, pkg string) (*ignoreMatcher, error) {
	candidates := []string{filepath.Join(stowDir, pkg, localIgnoreFile)}
	if home, err := homeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, globalIgnoreFile))
	}

	for _, file := range candidates {
		if _, err := os.Stat(file); err != nil {
			continue
		}
		patterns, err := readIgnoreFile(file)
		if err != nil {
			return nil, err
		}
		return newIgnoreMatcher(patterns)
	}
	return newIgnoreMatcher(builtinIgnorePatterns)
}

func homeDir() (string, error) {
	if home := os.Getenv("HOME"); home != "" {
		return home, nil
	}
	return os.UserHomeDir()
}

// readIgnoreFile parses an ignore file the way GNU Stow does: blank lines and
// comments are skipped, trailing comments are stripped, and `\#` unescapes.
func readIgnoreFile(name string) ([]string, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = trailingComment.ReplaceAllString(line, "")
		line = strings.ReplaceAll(line, `\#`, "#")
		if line != "" {
			patterns = append(patterns, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return patterns, nil
}

var trailingComment = regexp.MustCompile(`\s+#.*$`)

func normalizeOptions(opts Options) Options {
	if opts.StowDir == "" {
		opts.StowDir = "."
	}
	opts.StowDir = absOrClean(opts.StowDir)

	if opts.TargetDir == "" {
		// GNU Stow defaults the target to the parent of the stow directory,
		// which is the directory that contains the packages.
		opts.TargetDir = filepath.Dir(opts.StowDir)
	} else {
		opts.TargetDir = absOrClean(opts.TargetDir)
	}

	if opts.Verbose && opts.Writer == nil {
		opts.Writer = os.Stdout
	}
	return opts
}

// absOrClean resolves path to an absolute, cleaned path. Ownership checks and
// link comparisons require every path to be absolute; a relative path silently
// breaks them.
func absOrClean(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// pathInside reports whether path is dir itself or a descendant of dir.
func pathInside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// packageDir validates pkg and returns its absolute directory.
func packageDir(stowDir, pkg string) (string, error) {
	if pkg == "" {
		return "", errors.New("stow package name must not be empty")
	}
	dir := filepath.Join(stowDir, pkg)
	if dir == stowDir || !pathInside(stowDir, dir) {
		return "", fmt.Errorf("stow package %q must be inside the stow directory", pkg)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("stow package %q not accessible: %w", pkg, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("stow package %q is not a directory", dir)
	}
	return dir, nil
}

type nodeKind int

const (
	nodeDir nodeKind = iota
	nodeLink
)

// vnode is a path that will exist once the plan is executed. Planned nodes are
// overlaid on the real filesystem so nested unfolding sees the right state.
type vnode struct {
	kind nodeKind
	src  string // absolute link source for nodeLink
}

type vfs struct {
	nodes map[string]vnode
}

// statResult is the subset of file metadata the planner needs.
type statResult struct {
	exists    bool
	isDir     bool
	isSymlink bool
}

func (v *vfs) set(path string, n vnode) { v.nodes[filepath.Clean(path)] = n }

// deepestAncestor returns the longest planned node that is a proper ancestor of
// path, or "" when the real filesystem is authoritative.
func (v *vfs) deepestAncestor(path string) string {
	dir := filepath.Dir(path)
	for {
		if _, ok := v.nodes[dir]; ok {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func (v *vfs) lstat(target string) statResult {
	target = filepath.Clean(target)

	if n, ok := v.nodes[target]; ok {
		return statResult{exists: true, isDir: n.kind == nodeDir, isSymlink: n.kind == nodeLink}
	}

	anc := v.deepestAncestor(target)
	if anc == "" {
		fi, err := os.Lstat(target)
		if err != nil {
			return statResult{}
		}
		return statResult{exists: true, isDir: fi.IsDir(), isSymlink: fi.Mode()&os.ModeSymlink != 0}
	}

	n := v.nodes[anc]
	rel, err := filepath.Rel(anc, target)
	if err != nil {
		return statResult{}
	}

	if n.kind == nodeDir {
		// Fresh directory: only explicitly planned children exist.
		return statResult{}
	}

	fi, err := os.Lstat(filepath.Join(n.src, rel))
	if err != nil {
		return statResult{}
	}
	return statResult{exists: true, isDir: fi.IsDir(), isSymlink: fi.Mode()&os.ModeSymlink != 0}
}

// resolveLink turns a raw symlink payload into an absolute clean path.
func resolveLink(raw, linkPath string) string {
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(linkPath), raw))
}

// resolveLinkFromDisk resolves the symlink at path against the real filesystem.
func resolveLinkFromDisk(path string) (string, error) {
	raw, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	return resolveLink(raw, path), nil
}

// readlinkResolved returns the absolute destination of the symlink at path,
// taking planned links into account.
func (v *vfs) readlinkResolved(target string) (string, error) {
	target = filepath.Clean(target)

	if n, ok := v.nodes[target]; ok {
		if n.kind != nodeLink {
			return "", errors.New("not a planned symlink")
		}
		return n.src, nil
	}

	if anc := v.deepestAncestor(target); anc != "" {
		n := v.nodes[anc]
		rel, err := filepath.Rel(anc, target)
		if err != nil {
			return "", err
		}
		if n.kind == nodeDir {
			return "", errors.New("path does not exist")
		}
		return v.readlinkResolved(filepath.Join(n.src, rel))
	}

	raw, err := os.Readlink(target)
	if err != nil {
		return "", err
	}
	return resolveLink(raw, target), nil
}

// PlanStow computes the actions and conflicts for stowing pkg without
// modifying the filesystem.
func PlanStow(pkg string, opts Options) (*Plan, error) {
	s, err := newStower(pkg, opts)
	if err != nil {
		return nil, err
	}
	if err := s.stowDir(s.pkgDir, s.opts.TargetDir, ""); err != nil {
		return nil, err
	}
	return s.plan, nil
}

// Stow stows pkg from opts.StowDir into opts.TargetDir.
// If opts.Simulate is true, no filesystem changes are made.
// If conflicts are detected, *ConflictError is returned without applying changes.
func Stow(pkg string, opts Options) (*Plan, error) {
	opts = normalizeOptions(opts)

	s, err := newStower(pkg, opts)
	if err != nil {
		return nil, err
	}
	if err := s.stowDir(s.pkgDir, s.opts.TargetDir, ""); err != nil {
		return nil, err
	}

	printPlan(opts, s.plan)

	if len(s.plan.Conflicts) > 0 {
		return s.plan, &ConflictError{Package: pkg, Conflicts: s.plan.Conflicts}
	}
	if opts.Simulate {
		return s.plan, nil
	}

	if err := executeActions(s.plan.Actions); err != nil {
		return s.plan, err
	}
	return s.plan, nil
}

// Unstow removes symlinks in opts.TargetDir that belong to pkg and folds back
// directories that end up containing only one package's links.
func Unstow(pkg string, opts Options) (*Plan, error) {
	opts = normalizeOptions(opts)

	s, err := newStower(pkg, opts)
	if err != nil {
		return nil, err
	}
	if err := s.unstowDir(s.pkgDir, s.opts.TargetDir, ""); err != nil {
		return nil, err
	}

	printPlan(opts, s.plan)

	if opts.Simulate {
		return s.plan, nil
	}

	if err := executeActions(s.plan.Actions); err != nil {
		return s.plan, err
	}

	// Fold directories that now contain only one package's links, deepest
	// first. Like GNU Stow, pre-existing directories are never removed.
	sort.Slice(s.refoldDirs, func(i, j int) bool { return len(s.refoldDirs[i]) > len(s.refoldDirs[j]) })
	for _, dir := range s.refoldDirs {
		if err := s.refold(dir); err != nil {
			return s.plan, err
		}
	}
	return s.plan, nil
}

func printPlan(opts Options, plan *Plan) {
	if !opts.Verbose || opts.Writer == nil {
		return
	}
	for _, act := range plan.Actions {
		fmt.Fprintln(opts.Writer, act.String())
	}
}

type stower struct {
	pkg        string
	pkgDir     string
	opts       Options
	plan       *Plan
	fs         *vfs
	ignore     *ignoreMatcher
	refoldDirs []string
}

func newStower(pkg string, opts Options) (*stower, error) {
	opts = normalizeOptions(opts)

	pkgDir, err := packageDir(opts.StowDir, pkg)
	if err != nil {
		return nil, err
	}
	ignore, err := loadIgnoreMatcher(opts.StowDir, pkg)
	if err != nil {
		return nil, err
	}
	return &stower{
		pkg:    pkg,
		pkgDir: pkgDir,
		opts:   opts,
		plan:   &Plan{},
		fs:     &vfs{nodes: make(map[string]vnode)},
		ignore: ignore,
	}, nil
}

func (s *stower) matchesOverride(relPath string) bool {
	slashPath := filepath.ToSlash(relPath)
	for _, re := range s.opts.Override {
		if re != nil && re.MatchString(slashPath) {
			return true
		}
	}
	return false
}

func (s *stower) isSkipped(relPath string) bool {
	if len(s.opts.Skip) == 0 {
		return false
	}
	return s.opts.Skip[relPath] || s.opts.Skip[filepath.ToSlash(relPath)]
}

// excluded reports whether a package-relative path should not be processed.
func (s *stower) excluded(relPath string) bool {
	return s.isSkipped(relPath) || s.ignore.isIgnored(relPath)
}

// shouldSkipTarget protects the stow directory and directories marked .stow or
// .nonstow, matching GNU Stow's safety checks.
func (s *stower) shouldSkipTarget(dir string) bool {
	if dir == s.opts.StowDir {
		return true
	}
	return exists(filepath.Join(dir, ".stow")) || exists(filepath.Join(dir, ".nonstow"))
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// nodeExists follows symlinks, matching GNU Stow's -e semantics for deciding
// whether a link points at a real node.
func nodeExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (s *stower) isOwnedByStow(target string) bool {
	resolved, err := s.fs.readlinkResolved(target)
	if err != nil {
		return false
	}
	return pathInside(s.opts.StowDir, resolved)
}

func (s *stower) pointsTo(target, srcPath string) bool {
	resolved, err := s.fs.readlinkResolved(target)
	if err != nil {
		return false
	}
	return resolved == filepath.Clean(srcPath)
}

// linkDestination returns the raw payload of the symlink at path, which is what
// GNU Stow prints in conflict messages.
func (s *stower) linkDestination(target string) string {
	if n, ok := s.fs.nodes[filepath.Clean(target)]; ok && n.kind == nodeLink {
		if rel, err := filepath.Rel(filepath.Dir(target), n.src); err == nil {
			return rel
		}
		return n.src
	}
	raw, err := os.Readlink(target)
	if err != nil {
		return ""
	}
	return raw
}

// stowedToOtherPackage formats the conflict reason used when the existing link
// belongs to the stow directory but to a different package.
func (s *stower) stowedToOtherPackage(target, relPath string) string {
	return fmt.Sprintf("existing target is stowed to a different package: %s => %s",
		relPath, s.linkDestination(target))
}

func (s *stower) addLink(srcPath, dstPath string) {
	relTarget, err := filepath.Rel(filepath.Dir(dstPath), srcPath)
	if err != nil {
		relTarget = srcPath
	}
	absSrc, err := filepath.Abs(srcPath)
	if err != nil {
		absSrc = srcPath
	}
	s.plan.Actions = append(s.plan.Actions, Action{
		Kind:   ActionLink,
		Target: dstPath,
		Source: relTarget,
	})
	s.fs.set(dstPath, vnode{kind: nodeLink, src: filepath.Clean(absSrc)})
}

func (s *stower) replaceLink(srcPath, dstPath string) {
	s.plan.Actions = append(s.plan.Actions, Action{Kind: ActionRemove, Target: dstPath})
	delete(s.fs.nodes, filepath.Clean(dstPath))
	s.addLink(srcPath, dstPath)
}

func (s *stower) addConflict(srcPath, dstPath, relPath, reason string) {
	s.plan.Conflicts = append(s.plan.Conflicts, Conflict{
		RelPath:    relPath,
		TargetPath: dstPath,
		SourcePath: srcPath,
		Reason:     reason,
	})
}

func (s *stower) stowDir(srcDir, dstDir, relDir string) error {
	if s.shouldSkipTarget(dstDir) {
		return nil
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		name := entry.Name()
		srcPath := filepath.Join(srcDir, name)
		dstPath := filepath.Join(dstDir, name)
		relPath := name
		if relDir != "" {
			relPath = filepath.Join(relDir, name)
		}
		if s.excluded(relPath) {
			continue
		}

		info, err := os.Lstat(srcPath)
		if err != nil {
			return err
		}

		// Absolute symlinks are host-specific and cannot be unstowed, so GNU
		// Stow refuses to stow them.
		if info.Mode()&os.ModeSymlink != 0 {
			if raw, err := os.Readlink(srcPath); err == nil && filepath.IsAbs(raw) {
				s.addConflict(srcPath, dstPath, relPath,
					fmt.Sprintf("source is an absolute symlink %s => %s", srcPath, raw))
				continue
			}
		}

		isDir := info.IsDir()
		if info.Mode()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(srcPath); err == nil {
				isDir = fi.IsDir()
			}
		}

		dst := s.fs.lstat(dstPath)
		if isDir {
			if err := s.stowSubdir(srcPath, dstPath, relPath, dst); err != nil {
				return err
			}
			continue
		}
		if err := s.stowFile(srcPath, dstPath, relPath, dst); err != nil {
			return err
		}
	}
	return nil
}

func (s *stower) stowSubdir(srcPath, dstPath, relPath string, dst statResult) error {
	if !dst.exists {
		// Tree folding: link the whole directory when the target is absent.
		s.addLink(srcPath, dstPath)
		return nil
	}

	if dst.isSymlink {
		return s.stowOverLink(srcPath, dstPath, relPath, true)
	}

	if dst.isDir {
		return s.stowDir(srcPath, dstPath, relPath)
	}

	s.addConflict(srcPath, dstPath, relPath, fmt.Sprintf(
		"cannot stow %s/%s over existing target %s since neither a link nor a directory and --adopt not specified",
		s.pkg, relPath, relPath))
	return nil
}

func (s *stower) stowFile(srcPath, dstPath, relPath string, dst statResult) error {
	if !dst.exists {
		s.addLink(srcPath, dstPath)
		return nil
	}

	if dst.isSymlink {
		return s.stowOverLink(srcPath, dstPath, relPath, false)
	}

	if dst.isDir {
		s.addConflict(srcPath, dstPath, relPath, fmt.Sprintf(
			"cannot stow non-directory %s/%s over existing directory %s", s.pkg, relPath, relPath))
		return nil
	}

	if s.opts.Adopt {
		s.plan.Actions = append(s.plan.Actions, Action{
			Kind:   ActionAdopt,
			Target: dstPath,
			Source: srcPath,
		})
		s.addLink(srcPath, dstPath)
		return nil
	}

	s.addConflict(srcPath, dstPath, relPath, fmt.Sprintf(
		"cannot stow %s/%s over existing target %s since neither a link nor a directory and --adopt not specified",
		s.pkg, relPath, relPath))
	return nil
}

// stowOverLink resolves an existing symlink at the target. It returns after
// handling the path; srcIsDir selects whether the package entry is a directory.
func (s *stower) stowOverLink(srcPath, dstPath, relPath string, srcIsDir bool) error {
	if s.pointsTo(dstPath, srcPath) {
		return nil
	}

	// GNU Stow never overrides links it does not own, even with --override.
	if !s.isOwnedByStow(dstPath) {
		s.addConflict(srcPath, dstPath, relPath, fmt.Sprintf("existing target is not owned by stow: %s", relPath))
		return nil
	}

	resolved, err := s.fs.readlinkResolved(dstPath)
	if err != nil {
		return err
	}

	// A link pointing at a path that no longer exists is stale; replace it.
	if !nodeExists(resolved) {
		s.replaceLink(srcPath, dstPath)
		return nil
	}

	if s.matchesOverride(relPath) {
		s.replaceLink(srcPath, dstPath)
		return nil
	}

	if srcIsDir {
		if fi, err := os.Stat(resolved); err == nil && fi.IsDir() {
			// Tree unfolding: replace the folded link with a real directory
			// holding the previous package's entries, then merge.
			if err := s.planUnfold(dstPath, resolved, relPath); err != nil {
				return err
			}
			return s.stowDir(srcPath, dstPath, relPath)
		}
	}

	s.addConflict(srcPath, dstPath, relPath, s.stowedToOtherPackage(dstPath, relPath))
	return nil
}

func (s *stower) planUnfold(dstPath, existingSrcDir, relPath string) error {
	s.plan.Actions = append(s.plan.Actions, Action{
		Kind:   ActionUnfold,
		Target: dstPath,
	})
	s.fs.set(dstPath, vnode{kind: nodeDir})

	entries, err := os.ReadDir(existingSrcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		childRel := filepath.Join(relPath, name)
		if s.excluded(childRel) {
			continue
		}
		s.addLink(filepath.Join(existingSrcDir, name), filepath.Join(dstPath, name))
	}
	return nil
}

func (s *stower) unstowDir(srcDir, dstDir, relDir string) error {
	if s.shouldSkipTarget(dstDir) {
		return nil
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		name := entry.Name()
		srcPath := filepath.Join(srcDir, name)
		dstPath := filepath.Join(dstDir, name)
		relPath := name
		if relDir != "" {
			relPath = filepath.Join(relDir, name)
		}
		if s.excluded(relPath) {
			continue
		}

		srcIsDir := entry.IsDir()
		if info, err := os.Stat(srcPath); err == nil {
			srcIsDir = info.IsDir()
		}

		dst := s.fs.lstat(dstPath)
		if !dst.exists {
			continue
		}

		if dst.isSymlink {
			if s.pointsTo(dstPath, srcPath) {
				s.plan.Actions = append(s.plan.Actions, Action{
					Kind:   ActionRemove,
					Target: dstPath,
				})
			}
			continue
		}

		if srcIsDir && dst.isDir {
			if err := s.unstowDir(srcPath, dstPath, relPath); err != nil {
				return err
			}
			s.refoldDirs = append(s.refoldDirs, dstPath)
		}
	}
	return nil
}

// refold replaces dir with a single symlink to its sole source directory when
// every remaining entry is a link into the same package directory. GNU Stow
// folds the tree back after unstowing one of several packages, which can make
// other entries of that package visible through the new link.
func (s *stower) refold(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return nil
	}

	srcDir := ""
	for _, entry := range entries {
		target := filepath.Join(dir, entry.Name())
		fi, err := os.Lstat(target)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		resolved, err := resolveLinkFromDisk(target)
		if err != nil || !pathInside(s.opts.StowDir, resolved) {
			return nil
		}
		if parent := filepath.Dir(resolved); srcDir == "" {
			srcDir = parent
		} else if srcDir != parent {
			return nil
		}
	}

	rel, err := filepath.Rel(filepath.Dir(dir), srcDir)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.Symlink(rel, dir)
}

func executeActions(actions []Action) error {
	for _, act := range actions {
		switch act.Kind {
		case ActionUnfold:
			if err := os.Remove(act.Target); err != nil {
				return fmt.Errorf("unfold remove %s: %w", act.Target, err)
			}
			if err := os.MkdirAll(act.Target, 0777); err != nil {
				return fmt.Errorf("unfold mkdir %s: %w", act.Target, err)
			}
		case ActionRemove:
			if err := os.Remove(act.Target); err != nil {
				return fmt.Errorf("unlink %s: %w", act.Target, err)
			}
		case ActionAdopt:
			if err := os.MkdirAll(filepath.Dir(act.Source), 0777); err != nil {
				return fmt.Errorf("adopt mkdir %s: %w", filepath.Dir(act.Source), err)
			}
			if err := moveFile(act.Target, act.Source); err != nil {
				return fmt.Errorf("adopt move %s -> %s: %w", act.Target, act.Source, err)
			}
		case ActionLink:
			if err := os.MkdirAll(filepath.Dir(act.Target), 0777); err != nil {
				return fmt.Errorf("link parent mkdir %s: %w", filepath.Dir(act.Target), err)
			}
			if err := os.Symlink(act.Source, act.Target); err != nil {
				return fmt.Errorf("symlink %s -> %s: %w", act.Target, act.Source, err)
			}
		}
	}
	return nil
}

// moveFile moves src to dst, falling back to copy+remove when they live on
// different filesystems, as GNU Stow's File::Copy::move does.
func moveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	return copyAndRemove(src, dst)
}

func copyAndRemove(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Remove(src)
}

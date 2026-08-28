package fest

import "github.com/emad-elsaid/types"

import (
	"log/slog"
	"strings"

	"github.com/hashicorp/go-version"
)

const ResourceRubyGems ResourceName = "Ruby gems"

// listGemsRuby enumerates gems installed under the user's home, printing
// name@version and appending the platform suffix for native (non-ruby) gems.
var listGemsRuby = `require 'rubygems'
Gem::Specification.each do |s|
  next unless s.base_dir.include?(ENV['HOME'])
  platform = s.platform.to_s
  if platform == 'ruby'
    puts "#{s.name}@#{s.version}"
  else
    puts "#{s.name}@#{s.version} #{platform}"
  end
end
`

var (
	wantedRubyGems []string
	rubyListCache  listCache
)

// RubyGem declares Ruby gems to install for the current user.
// Supports version pinning and version constraints:
//   - gem@1.0.0: Exact version
//   - gem@~>1.0: Pessimistic version constraint (>= 1.0, < 2.0)
//   - gem@>=1.0.0: Minimum version
//
// Example:
//
//	fest.RubyGem(
//	    "bundler",
//	    "rails@7.0.0",
//	    "puma@~>5.0",
//	)
func RubyGem(gems ...string) { addUnique(&wantedRubyGems, gems...) }

type rubyGems struct{}

func (r rubyGems) Wanted() []string     { return wantedRubyGems }
func (r rubyGems) ResourceName() string { return string(ResourceRubyGems) }

func (r rubyGems) Match(want, have string) bool {
	wantName, wantVer := splitVer(want)
	haveName, haveVer := splitVer(have)
	if wantName != haveName {
		return false
	}
	wantVer = strings.TrimSpace(wantVer)
	haveVer = strings.TrimSpace(haveVer)
	// Match is symmetric for empty/latest versions
	if wantVer == "" || wantVer == "latest" || haveVer == "" || haveVer == "latest" {
		return true
	}
	if after, ok := strings.CutPrefix(wantVer, "~>"); ok {
		return r.matchPessimistic(after, haveVer)
	}
	if after, ok := strings.CutPrefix(wantVer, ">="); ok {
		return r.compareVer(haveVer, after) >= 0
	}
	if after, ok := strings.CutPrefix(wantVer, "="); ok {
		wantVer = after
	}
	return wantVer == haveVer
}

func (r rubyGems) matchPessimistic(want, have string) bool {
	want = strings.TrimSpace(want)

	// Check lower bound: have >= want
	if r.compareVer(have, want) < 0 {
		return false
	}

	// Calculate upper bound by incrementing component before last
	// ~>1.2 means >= 1.2, < 2.0
	// ~>1.2.3 means >= 1.2.3, < 1.3.0
	// ~>5 means >= 5.0, < 6.0
	wantParts := strings.Split(want, ".")
	upperParts := make([]string, len(wantParts))
	copy(upperParts, wantParts)

	// Increment the component before the last one (or the only one for single component)
	incrementIdx := len(wantParts) - 1
	if len(wantParts) > 1 {
		incrementIdx = len(wantParts) - 2
	}

	// Parse the component as integer, increment, convert back
	var num int
	for _, c := range upperParts[incrementIdx] {
		if c >= '0' && c <= '9' {
			num = num*10 + int(c-'0')
		} else {
			break
		}
	}
	num++

	// Format number back to string
	numStr := ""
	if num == 0 {
		numStr = "0"
	} else {
		temp := num
		for temp > 0 {
			numStr = string(rune('0'+temp%10)) + numStr
			temp /= 10
		}
	}
	upperParts[incrementIdx] = numStr

	// Set all components after incrementIdx to 0
	for i := incrementIdx + 1; i < len(upperParts); i++ {
		upperParts[i] = "0"
	}

	upperBound := strings.Join(upperParts, ".")

	// Check upper bound: have < upperBound
	return r.compareVer(have, upperBound) < 0
}

func (r rubyGems) compareVer(v1, v2 string) int {
	ver1, err1 := version.NewVersion(v1)
	ver2, err2 := version.NewVersion(v2)
	if err1 != nil || err2 != nil {
		return strings.Compare(v1, v2)
	}
	return ver1.Compare(ver2)
}

func (r rubyGems) ListInstalled() ([]string, error) {
	return rubyListCache.getOrSet(func() ([]string, error) {
		if _, err := types.Cmd("ruby", "--version").StdoutErr(); err != nil {
			slog.Debug("ruby is not installed or not available")
			return []string{}, nil
		}

		stdout, err := types.Cmd("ruby", "-e", listGemsRuby).StdoutErr()
		if err != nil {
			return nil, err
		}

		var gems []string
		for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				gems = append(gems, line)
			}
		}
		return gems, nil
	})
}

func (r rubyGems) ListExplicit() ([]string, error) {
	return r.ListInstalled()
}

func (r rubyGems) Install(gems []string) error {
	defer rubyListCache.invalidate()

	if _, err := types.Cmd("gem", "--version").StdoutErr(); err != nil {
		slog.Warn("gem is not installed, skipping Ruby gem installation")
		return nil
	}

	for _, gem := range gems {
		gemName := gem
		version := ""
		if idx := strings.Index(gem, "@"); idx != -1 {
			gemName = gem[:idx]
			version = gem[idx+1:]
		}

		args := []string{"install", gemName}
		if version != "" && version != "latest" {
			args = append(args, "--version", version)
		}

		slog.Info("Installing Ruby gem", "gem", gem)
		if err := types.Cmd("gem", args...).Interactive().Error(); err != nil {
			return err
		}
	}
	return nil
}

func (r rubyGems) Uninstall(gems []string) error {
	defer rubyListCache.invalidate()

	if _, err := types.Cmd("gem", "--version").StdoutErr(); err != nil {
		return nil
	}

	for _, gem := range gems {
		gemName := gem
		if idx := strings.Index(gem, "@"); idx != -1 {
			gemName = gem[:idx]
		}

		slog.Info("Uninstalling Ruby gem", "gem", gemName)
		if err := types.Cmd("gem", "uninstall", gemName, "--executables", "--ignore-dependencies").Interactive().Error(); err != nil {
			return err
		}
	}
	return nil
}

func (r rubyGems) MarkExplicit([]string) error                   { return nil }
func (r rubyGems) GetDependencies() (map[string][]string, error) { return nil, nil }

func (r rubyGems) SaveAsGo(wanted []string) error {
	installed, err := r.ListExplicit()
	if err != nil {
		return err
	}

	diff := subtract(r, installed, wanted)
	if len(diff) == 0 {
		logSuccess("No new ruby gems to save")
		return nil
	}

	if err := saveAsGoFile("ruby_gems.go", "RubyGem", diff); err != nil {
		return err
	}
	logSuccess("ruby gems saved", "file", "ruby_gems.go", "count", len(diff))
	return nil
}

package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// packageRuntimeDir returns the absolute directory holding runtime-installed
// packages (npm-global, pip, GitHub binaries, update caches).
func packageRuntimeDir() string {
	return absRuntimePath(rawPackageRuntimeDir())
}

// absRuntimePath anchors a runtime path to an absolute location. On Windows the
// "/app/data/.runtime" default (and any relative override) is drive-relative:
// "\app\data\.runtime" resolves against the drive of whichever working directory
// a child process runs in. Skill dep checks run node/python with cmd.Dir set to
// the skill's scripts dir (e.g. on C:) while installs ran from the gateway's cwd
// (e.g. A:), so the checker looked in a different tree than the installer wrote
// to. filepath.Abs pins it once, to the gateway process's own drive/cwd.
func absRuntimePath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func rawPackageRuntimeDir() string {
	if v := strings.TrimSpace(os.Getenv("RUNTIME_DIR")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("GOCLAW_DATA_DIR")); v != "" {
		return filepath.Join(v, ".runtime")
	}
	if runtime.GOOS != "windows" && !IsAlpineRuntime() {
		return "/var/lib/goclaw/data/.runtime"
	}
	return filepath.Join("/app/data", ".runtime")
}

func npmGlobalPrefix() string {
	if v := strings.TrimSpace(os.Getenv("NPM_CONFIG_PREFIX")); v != "" {
		return v
	}
	return filepath.Join(packageRuntimeDir(), "npm-global")
}

func npmGlobalBinDir() string {
	if runtime.GOOS == "windows" {
		return npmGlobalPrefix()
	}
	return filepath.Join(npmGlobalPrefix(), "bin")
}

func RuntimeExecutableDirs() []string {
	return uniqueNonEmptyPaths(
		filepath.Join(packageRuntimeDir(), "bin"),
		npmGlobalBinDir(),
		filepath.Join(packageRuntimeDir(), "pip", "bin"),
	)
}

func FindRuntimeExecutable(name string) (string, bool) {
	if strings.ContainsAny(name, `/\`) {
		return "", false
	}
	for _, dir := range RuntimeExecutableDirs() {
		path := filepath.Join(dir, name)
		if IsExecutableFile(path) {
			return path, true
		}
	}
	if path, ok := findNpmPackageExecutableAlias(name); ok {
		return path, true
	}
	return "", false
}

// NormalizeBinaryName returns the identity of a binary reference: its lowercased
// file base, with the Windows executable suffix removed. Callers compare binary
// identity through this helper so a registered name ("goclaw") and a resolved
// executable path (`C:\bin\goclaw.exe`) agree on every platform.
// Examples: "/usr/bin/gh" → "gh", "./GH" → "gh", `C:\bin\gh.exe` → "gh" (Windows).
func NormalizeBinaryName(s string) string {
	return normalizeBinaryNameFor(runtime.GOOS, s)
}

// normalizeBinaryNameFor is NormalizeBinaryName with an explicit GOOS so both
// platform branches stay testable on any host.
func normalizeBinaryNameFor(goos, s string) string {
	name := filepath.Base(strings.TrimSpace(strings.ToLower(s)))
	if goos == "windows" {
		name = strings.TrimSuffix(name, ".exe")
	}
	return name
}

func IsExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0111 != 0
}

// npmGlobalNodePath returns the directory where `npm install -g` places packages
// under the runtime prefix. npm uses <prefix>/lib/node_modules on Unix but
// <prefix>\node_modules on Windows (bins live directly in <prefix> there).
func npmGlobalNodePath() string {
	return npmGlobalNodePathFor(runtime.GOOS, npmGlobalPrefix())
}

// npmGlobalNodePathFor is npmGlobalNodePath with an explicit GOOS and prefix so
// both platform layouts stay testable on any host.
func npmGlobalNodePathFor(goos, prefix string) string {
	if goos == "windows" {
		return filepath.Join(prefix, "node_modules")
	}
	return filepath.Join(prefix, "lib", "node_modules")
}

func ensureNpmGlobalEnv() {
	prependProcessPath(npmGlobalBinDir())
}

func npmCommandEnv() []string {
	prefix := npmGlobalPrefix()
	binDir := npmGlobalBinDir()
	nodePath := npmGlobalNodePath()

	env := make([]string, 0, len(os.Environ())+3)
	for _, e := range os.Environ() {
		switch {
		case strings.HasPrefix(e, "NPM_CONFIG_PREFIX="):
			continue
		case strings.HasPrefix(e, "PATH="):
			continue
		case strings.HasPrefix(e, "NODE_PATH="):
			continue
		}
		env = append(env, e)
	}

	pathValue := prependPathValue(os.Getenv("PATH"), binDir)
	nodePathValue := prependPathValue(os.Getenv("NODE_PATH"), nodePath)
	env = append(env,
		"NPM_CONFIG_PREFIX="+prefix,
		"PATH="+pathValue,
		"NODE_PATH="+nodePathValue,
	)
	return env
}

func prependProcessPath(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	_ = os.Setenv("PATH", prependPathValue(os.Getenv("PATH"), dir))
}

func prependPathValue(current, dir string) string {
	if strings.TrimSpace(dir) == "" {
		return current
	}
	parts := filepath.SplitList(current)
	for _, p := range parts {
		if p == dir {
			return current
		}
	}
	if current == "" {
		return dir
	}
	return dir + string(os.PathListSeparator) + current
}

func uniqueNonEmptyPaths(paths ...string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

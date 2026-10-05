package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// normalizeBinaryNameFor's .exe-stripping branch only matters on Windows,
// but bare basenames (no separators) let us exercise both GOOS branches
// deterministically regardless of the host running the test — unlike
// filepath.Base, which only splits on the native separator.
func TestNormalizeBinaryNameForStripsExeOnWindowsOnly(t *testing.T) {
	cases := []struct {
		goos string
		in   string
		want string
	}{
		{"windows", "goclaw.exe", "goclaw"},
		{"windows", "GOCLAW.EXE", "goclaw"},
		{"windows", "goclaw", "goclaw"},
		{"linux", "goclaw.exe", "goclaw.exe"},
		{"darwin", "goclaw", "goclaw"},
	}
	for _, c := range cases {
		if got := normalizeBinaryNameFor(c.goos, c.in); got != c.want {
			t.Errorf("normalizeBinaryNameFor(%q, %q) = %q, want %q", c.goos, c.in, got, c.want)
		}
	}
}

func TestNpmCommandEnvUsesRuntimePrefix(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("RUNTIME_DIR", runtimeDir)
	t.Setenv("NPM_CONFIG_PREFIX", "")
	t.Setenv("NODE_PATH", "")
	t.Setenv("PATH", "/usr/bin")

	env := npmCommandEnv()
	wantPrefix := filepath.Join(runtimeDir, "npm-global")
	wantBin := npmGlobalBinDir()
	wantNodePath := npmGlobalNodePathFor(runtime.GOOS, wantPrefix)

	if !envContainsExact(env, "NPM_CONFIG_PREFIX="+wantPrefix) {
		t.Fatalf("npmCommandEnv missing NPM_CONFIG_PREFIX=%q", wantPrefix)
	}
	if !envContainsPrefixValue(env, "PATH=", wantBin) {
		t.Fatalf("npmCommandEnv PATH does not start with %q", wantBin)
	}
	if !envContainsPrefixValue(env, "NODE_PATH=", wantNodePath) {
		t.Fatalf("npmCommandEnv NODE_PATH does not start with %q", wantNodePath)
	}
}

func TestEnsureNpmGlobalEnvPrependsProcessPath(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("RUNTIME_DIR", runtimeDir)
	t.Setenv("NPM_CONFIG_PREFIX", "")
	t.Setenv("PATH", "/usr/bin")

	ensureNpmGlobalEnv()

	wantBin := npmGlobalBinDir()
	if got := os.Getenv("PATH"); !strings.HasPrefix(got, wantBin+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q, want prefix %q", got, wantBin)
	}
}

func TestFindRuntimeExecutableFindsNpmGlobalBinary(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("RUNTIME_DIR", runtimeDir)
	t.Setenv("NPM_CONFIG_PREFIX", "")

	binDir := npmGlobalBinDir()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(binDir, "openrouter")
	if err := os.WriteFile(wantPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := FindRuntimeExecutable("openrouter")
	if !ok {
		t.Fatalf("FindRuntimeExecutable did not find npm global binary")
	}
	if got != wantPath {
		t.Fatalf("FindRuntimeExecutable path = %q, want %q", got, wantPath)
	}
}

func TestFindRuntimeExecutableFindsGoogleWorkspaceBinary(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("RUNTIME_DIR", runtimeDir)
	t.Setenv("NPM_CONFIG_PREFIX", "")

	binDir := npmGlobalBinDir()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(binDir, "gws")
	if err := os.WriteFile(wantPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := FindRuntimeExecutable("gws")
	if !ok {
		t.Fatalf("FindRuntimeExecutable did not find gws runtime binary")
	}
	if got != wantPath {
		t.Fatalf("FindRuntimeExecutable path = %q, want %q", got, wantPath)
	}
}

func TestFindRuntimeExecutableFindsNpmPackageCliAlias(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("RUNTIME_DIR", runtimeDir)
	t.Setenv("NPM_CONFIG_PREFIX", "")

	pkgDir := filepath.Join(npmGlobalNodePath(), "openrouter-cli")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"name":"openrouter-cli","bin":{"orc":"dist/index.js"}}`)
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := npmGlobalBinDir()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(binDir, "orc")
	if err := os.WriteFile(wantPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := FindRuntimeExecutable("openrouter")
	if !ok {
		t.Fatalf("FindRuntimeExecutable did not find npm package CLI alias")
	}
	if got != wantPath {
		t.Fatalf("FindRuntimeExecutable alias path = %q, want %q", got, wantPath)
	}
}

func TestFullImagePreinstallsGoogleWorkspaceCLI(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(raw)
	if !strings.Contains(dockerfile, "ENABLE_FULL_SKILLS") {
		t.Fatal("Dockerfile contract test cannot locate ENABLE_FULL_SKILLS block")
	}
	if !strings.Contains(dockerfile, "@googleworkspace/cli@0.22.5") {
		t.Fatal("full runtime image must preinstall @googleworkspace/cli@0.22.5")
	}
}

func TestFindRuntimeExecutableRejectsPathLikeNames(t *testing.T) {
	if got, ok := FindRuntimeExecutable("../openrouter"); ok || got != "" {
		t.Fatalf("FindRuntimeExecutable path-like name = %q, %v; want empty false", got, ok)
	}
}

func envContainsExact(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}

func envContainsPrefixValue(env []string, key, wantPrefix string) bool {
	for _, item := range env {
		if strings.HasPrefix(item, key) {
			return strings.HasPrefix(strings.TrimPrefix(item, key), wantPrefix)
		}
	}
	return false
}

func TestNpmGlobalNodePathForMatchesNpmLayout(t *testing.T) {
	prefix := filepath.Join("rt", "npm-global")
	if got, want := npmGlobalNodePathFor("windows", prefix), filepath.Join(prefix, "node_modules"); got != want {
		t.Errorf("windows: npmGlobalNodePathFor = %q, want %q", got, want)
	}
	for _, goos := range []string{"linux", "darwin"} {
		if got, want := npmGlobalNodePathFor(goos, prefix), filepath.Join(prefix, "lib", "node_modules"); got != want {
			t.Errorf("%s: npmGlobalNodePathFor = %q, want %q", goos, got, want)
		}
	}
}

func TestPackageRuntimeDirIsAbsolute(t *testing.T) {
	t.Setenv("RUNTIME_DIR", "")
	t.Setenv("GOCLAW_DATA_DIR", "")
	if got := packageRuntimeDir(); !filepath.IsAbs(got) {
		t.Errorf("default packageRuntimeDir() = %q, want an absolute path (drive-qualified on Windows)", got)
	}

	t.Setenv("RUNTIME_DIR", filepath.Join("rel", "rt"))
	got := packageRuntimeDir()
	want, err := filepath.Abs(filepath.Join("rel", "rt"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("relative RUNTIME_DIR: packageRuntimeDir() = %q, want %q", got, want)
	}

	abs := t.TempDir()
	t.Setenv("RUNTIME_DIR", abs)
	if got := packageRuntimeDir(); got != abs {
		t.Errorf("absolute RUNTIME_DIR: packageRuntimeDir() = %q, want %q unchanged", got, abs)
	}
}
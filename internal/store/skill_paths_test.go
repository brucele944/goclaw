package store

import (
	"path/filepath"
	"testing"
)

// These paths are built via filepath.FromSlash/Join rather than hardcoded
// POSIX literals: SkillBaseDir/SkillMarkdownPath/SkillSlugDir normalize with
// path/filepath (OS-native) since every real caller persists file_path via
// filepath.Join, so on Windows a forward-slash literal expectation never
// matches the (correct) backslash-normalized output.

func TestSkillBaseDirAcceptsDirectoryPath(t *testing.T) {
	dir := filepath.FromSlash("/var/lib/goclaw/data/skills-store/demo/3")
	want := filepath.Clean(dir)
	if got := SkillBaseDir(dir); got != want {
		t.Fatalf("SkillBaseDir() = %q, want %q", got, want)
	}
}

func TestSkillBaseDirAcceptsSkillMarkdownPath(t *testing.T) {
	dir := filepath.FromSlash("/var/lib/goclaw/data/skills-store/demo/3")
	md := filepath.Join(dir, SkillMarkdownFilename)
	want := filepath.Clean(dir)
	if got := SkillBaseDir(md); got != want {
		t.Fatalf("SkillBaseDir() = %q, want %q", got, want)
	}
}

func TestSkillMarkdownPath(t *testing.T) {
	dir := filepath.FromSlash("/var/lib/goclaw/data/skills-store/demo/3")
	md := filepath.Join(dir, SkillMarkdownFilename)
	if got := SkillMarkdownPath(md); got != md {
		t.Fatalf("SkillMarkdownPath() = %q, want %q", got, md)
	}
}

func TestSkillSlugDir(t *testing.T) {
	dir := filepath.FromSlash("/var/lib/goclaw/data/skills-store/demo/3")
	md := filepath.Join(dir, SkillMarkdownFilename)
	want := filepath.Dir(filepath.Clean(dir))
	if got := SkillSlugDir(md); got != want {
		t.Fatalf("SkillSlugDir() = %q, want %q", got, want)
	}
}

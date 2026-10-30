package betterzip

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func settingsHome(t *testing.T) string {
	t.Helper()
	home := testDir(t)
	t.Setenv("HOME", home)
	t.Setenv("BETTERZIP_MCP_ROOT", "")
	return home
}

func TestRootSettingsAndPrecedence(t *testing.T) {
	home := settingsHome(t)
	if root, _, err := ResolveRoot(nil); err != nil || root != "" {
		t.Fatalf("missing settings must not grant access: %q, %v", root, err)
	}
	data := filepath.Join(home, "资料 folder")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	filename, err := SaveRoot(data)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, []byte(home)) || !bytes.Contains(contents, []byte("~/资料 folder")) {
		t.Fatalf("settings must use a home-relative path: %s", contents)
	}
	info, err := os.Stat(filename)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private settings permissions: %v, %v", info, err)
	}
	root, source, err := ResolveRoot(nil)
	if err != nil || root != "~/资料 folder" || source != "用户设置" {
		t.Fatalf("saved settings: %q, %q, %v", root, source, err)
	}
	service := testService(t, root)
	if service.RootPath() != data {
		t.Fatalf("saved setting resolved to an unexpected directory")
	}
	t.Setenv("BETTERZIP_MCP_ROOT", "~/environment")
	if root, source, err = ResolveRoot(nil); err != nil || root != "~/environment" || source != "BETTERZIP_MCP_ROOT" {
		t.Fatalf("environment precedence: %q, %q, %v", root, source, err)
	}
	if root, source, err = ResolveRoot(new("~/explicit")); err != nil || root != "~/explicit" || source != "--root" {
		t.Fatalf("flag precedence: %q, %q, %v", root, source, err)
	}
	if _, _, err := ResolveRoot(new("")); err == nil {
		t.Fatal("an explicitly empty flag must not fall back to another root")
	}
}

func TestBadSettingsNeverFallBack(t *testing.T) {
	settingsHome(t)
	filename, err := SettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`{`, `{}`, `{"root":""}`, `{"root":2}`, `{"root":"."}`, `{"root":"relative/path"}`, `{"root":"~/data","unknown":true}`, `{"root":"~/one","root":"~/two"}`, strings.Repeat(" ", maxSettingsBytes+1)} {
		if err := os.WriteFile(filename, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if root, _, err := ResolveRoot(nil); err == nil || root != "" {
			t.Fatal("malformed configuration must not grant an implicit root")
		}
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing.json", filename); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveRoot(nil); err == nil {
		t.Fatal("symbolic-link configuration must be rejected")
	}
}

func TestFailedSavePreservesSettings(t *testing.T) {
	home := settingsHome(t)
	for _, name := range []string{"old", "new"} {
		if err := os.Mkdir(filepath.Join(home, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	filename, err := SaveRoot("~/old")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SaveRoot("~/missing"); err == nil {
		t.Fatal("nonexistent directory was saved")
	}
	after, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("invalid input changed the previous settings")
	}
	if os.Geteuid() != 0 {
		dir := filepath.Dir(filename)
		if err := os.Chmod(dir, 0500); err != nil {
			t.Fatal(err)
		}
		_, saveErr := SaveRoot("~/new")
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if saveErr == nil {
			t.Fatal("saving to a read-only settings directory unexpectedly succeeded")
		}
		after, err = os.ReadFile(filename)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("write failure changed the previous settings")
		}
	}
	if _, err := SaveRoot("~/new"); err != nil {
		t.Fatal(err)
	}
	if root, _, err := ResolveRoot(nil); err != nil || root != "~/new" {
		t.Fatalf("reconfiguration: %q, %v", root, err)
	}
	entries, err := os.ReadDir(filepath.Dir(filename))
	if err != nil || len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatal("temporary configuration files were not cleaned")
	}
}

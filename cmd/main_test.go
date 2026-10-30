package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gtkit/betterzip-mcp/internal"
)

func TestSetupInteractionAndCancellation(t *testing.T) {
	home, err := os.MkdirTemp(".", ".test-setup-")
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(home); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("HOME", home)
	t.Setenv("BETTERZIP_MCP_ROOT", "")
	if err := os.Mkdir(filepath.Join(home, "资料"), 0700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runSetup(nil, strings.NewReader("\n"), &output); err == nil {
		t.Fatal("blank input should cancel setup")
	}
	if root, _, err := betterzip.ResolveRoot(nil); err != nil || root != "" {
		t.Fatal("cancellation wrote settings")
	}
	output.Reset()
	if err := runSetup(nil, strings.NewReader("~/资料\n"), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "已保存") {
		t.Fatal("setup did not confirm the saved selection")
	}
	if root, _, err := betterzip.ResolveRoot(nil); err != nil || root != "~/资料" {
		t.Fatalf("interactive selection: %q, %v", root, err)
	}
	output.Reset()
	if err := runSetup([]string{"--root", "~/missing"}, strings.NewReader(""), &output); err == nil {
		t.Fatal("invalid directory should fail")
	}
	if root, _, err := betterzip.ResolveRoot(nil); err != nil || root != "~/资料" {
		t.Fatal("failed setup replaced the previous selection")
	}
}

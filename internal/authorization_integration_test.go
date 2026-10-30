//go:build integration

package betterzip

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestFirstUseArchiveAndReconfigure(t *testing.T) {
	a := testAuthorization(t, nil)
	first, second := authorizationDir(t), authorizationDir(t)
	selected := first
	selections := 0
	a.picker = func(context.Context) (string, error) { selections++; return selected, nil }
	for _, directory := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(directory, "hello.txt"), []byte(filepath.Base(directory)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := a.server()
	call := func(name string, args map[string]any) {
		t.Helper()
		result, err := s.GetTool(name).Handler(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
		if err != nil || result == nil || result.IsError {
			t.Fatalf("%s failed: %v, %+v", name, err, result)
		}
	}
	compress := map[string]any{"working_dir": ".", "output_archive": "data.zip", "sources": []string{"hello.txt"}}
	call("betterzip_compress", compress)
	if selections != 1 {
		t.Fatal("first use did not prompt exactly once")
	}
	call("betterzip_extract", map[string]any{"archive_path": "data.zip", "output_dir": "out"})
	got, err := os.ReadFile(filepath.Join(first, "out", "hello.txt"))
	if err != nil || string(got) != filepath.Base(first) {
		t.Fatal("first-use ZIP did not round-trip")
	}
	if selections != 1 {
		t.Fatal("saved selection prompted again")
	}
	selected = second
	call("betterzip_configure", map[string]any{})
	call("betterzip_compress", compress)
	if _, err := os.Stat(filepath.Join(second, "data.zip")); err != nil {
		t.Fatal("changed root did not take effect:", err)
	}
	denied, err := s.GetTool("betterzip_compress").Handler(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{
		"working_dir": first, "output_archive": "denied.zip", "sources": []string{"hello.txt"},
	}}})
	if err != nil || !denied.IsError {
		t.Fatal("old root remained authorized for new operations")
	}
	if _, err := os.Stat(filepath.Join(first, "denied.zip")); !os.IsNotExist(err) {
		t.Fatal("unauthorized archive was written")
	}
}

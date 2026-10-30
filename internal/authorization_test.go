package betterzip

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/mark3labs/mcp-go/mcp"
)

func testAuthorization(t *testing.T, picker func(context.Context) (string, error)) *authorization {
	t.Helper()
	settingsHome(t)
	return &authorization{config: Config{Timeout: DefaultTimeout, MaxBytes: DefaultMaxBytes}, picker: picker}
}

func authorizationDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(home, "data-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAuthorizationPersistsAndChangesWithoutClosingActiveRoot(t *testing.T) {
	var selected string
	var calls int
	a := testAuthorization(t, func(context.Context) (string, error) { calls++; return selected, nil })
	first, second := authorizationDir(t), authorizationDir(t)
	selected = first
	old, err := a.open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := old.Close(); err != nil {
			t.Error(err)
		}
	})
	if calls != 1 || old.RootPath() != first {
		t.Fatal("first use did not select the authorized root")
	}
	filename, err := SettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filename)
	if err != nil || bytes.Contains(contents, []byte(os.Getenv("HOME"))) || !bytes.Contains(contents, []byte("~/data-")) {
		t.Fatal("test configuration must preserve home-relative paths")
	}
	reopened, err := a.open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("saved selection prompted again")
	}
	selected = second
	if root, err := a.selectRoot(t.Context(), true); err != nil || root != second {
		t.Fatalf("change selection: %v", err)
	}
	current, err := a.open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := current.Close(); err != nil {
			t.Error(err)
		}
	})
	if current.RootPath() != second {
		t.Fatal("new operation used the previous root")
	}
	if _, err := old.root.Stat("."); err != nil {
		t.Fatal("changing selection closed an active operation root")
	}
	// A new process uses the same persisted selection without invoking a picker.
	fresh := &authorization{config: a.config, picker: func(context.Context) (string, error) {
		t.Fatal("persisted root was not reused")
		return "", nil
	}}
	saved, err := fresh.open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := saved.Close(); err != nil {
			t.Error(err)
		}
	})
	if saved.RootPath() != second {
		t.Fatal("restart did not use the changed selection")
	}
}

func TestAuthorizationFailuresPreserveSavedChoice(t *testing.T) {
	a := testAuthorization(t, nil)
	old := authorizationDir(t)
	filename, err := SaveRoot(old)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	failed := errors.New("picker unavailable")
	for _, tc := range []struct {
		name string
		root string
		err  error
	}{
		{"cancel", "", errSelectionCanceled},
		{"timeout", "", context.DeadlineExceeded},
		{"picker-error", "", failed},
		{"empty-selection", "", nil},
		{"invalid-selection", filepath.Join(old, "missing"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.picker = func(context.Context) (string, error) { return tc.root, tc.err }
			if _, err := a.selectRoot(t.Context(), true); err == nil {
				t.Fatal("failed choice was accepted")
			}
			after, err := os.ReadFile(filename)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed choice changed saved settings")
			}
		})
	}
	a.picker = func(context.Context) (string, error) { return authorizationDir(t), nil }
	if _, err := a.selectRoot(t.Context(), true); err != nil {
		t.Fatal("a failed picker prevented a later retry:", err)
	}
}

func TestAuthorizationCanceledBeforeSave(t *testing.T) {
	a := testAuthorization(t, nil)
	root := authorizationDir(t)
	ctx, cancel := context.WithCancel(t.Context())
	a.picker = func(context.Context) (string, error) { cancel(); return root, nil }
	if _, err := a.open(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled selection: %v", err)
	}
	if root, _, err := ResolveRoot(nil); err != nil || root != "" {
		t.Fatal("canceled request persisted a selection")
	}
}

func TestAuthorizationConcurrentFirstUseSharesSelection(t *testing.T) {
	a := testAuthorization(t, nil)
	root := authorizationDir(t)
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		a.picker = func(ctx context.Context) (string, error) {
			calls.Add(1)
			select {
			case <-release:
				return root, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		var wg sync.WaitGroup
		for range 6 {
			wg.Go(func() {
				s, err := a.open(t.Context())
				if err != nil {
					t.Error(err)
					return
				}
				if s.RootPath() != root {
					t.Error("incorrect shared selection")
				}
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("concurrent requests opened multiple pickers")
		}
		ctx, cancel := context.WithCancel(t.Context())
		waiter := make(chan error, 1)
		wg.Go(func() {
			s, err := a.open(ctx)
			if s != nil {
				if closeErr := s.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			}
			waiter <- err
		})
		synctest.Wait()
		cancel()
		if err := <-waiter; !errors.Is(err, context.Canceled) {
			t.Fatal("waiting request ignored cancellation")
		}
		close(release)
		wg.Wait()
		if calls.Load() != 1 {
			t.Fatal("saved selection prompted again")
		}
	})
}

func TestAuthorizationConfigurationToolSavesNativeSelection(t *testing.T) {
	a := testAuthorization(t, nil)
	selected := authorizationDir(t)
	a.picker = func(context.Context) (string, error) { return selected, nil }
	s := a.server()
	result, err := s.GetTool("betterzip_configure").Handler(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{}}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("configuration tool failed: %v, %+v", err, result)
	}
	current, err := a.open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if current.RootPath() != selected {
		t.Error("tool did not persist the selected root")
	}
	if err := current.Close(); err != nil {
		t.Error(err)
	}
}

func TestArchiveToolReleasesRequestRoot(t *testing.T) {
	a := testAuthorization(t, nil)
	for _, outcome := range []string{"success", "error", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			config := a.config
			config.Root = authorizationDir(t)
			s, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				result := archiveTool(t.Context(), func(context.Context) (*Service, bool, error) {
					return s, true, nil
				}, "压缩完成：", func(*Service) (string, error) {
					switch outcome {
					case "error":
						return "", ErrInvalid
					case "panic":
						panic("controlled operation panic")
					default:
						return "output.zip", nil
					}
				})
				if result.IsError != (outcome == "error") {
					t.Error("operation result changed")
				}
			}()
			if panicked != (outcome == "panic") {
				t.Error("unexpected panic result")
			}
			if _, err := s.root.Stat("."); !errors.Is(err, os.ErrClosed) {
				t.Error("request root was not closed")
			}
		})
	}
}

func TestAuthorizationConfigurationToolRejectsModelPathsAndOverrides(t *testing.T) {
	var calls int
	a := testAuthorization(t, func(context.Context) (string, error) { calls++; return "", errSelectionCanceled })
	s := a.server()
	if len(s.ListTools()) != 5 || calls != 0 {
		t.Fatal("discovery must expose five tools without prompting")
	}
	tool := s.GetTool("betterzip_configure")
	result, err := tool.Handler(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"root": "/"}}})
	if err != nil || !result.IsError || calls != 0 {
		t.Fatal("a model-supplied root was accepted")
	}
	for _, useEnvironment := range []bool{false, true} {
		a.override = nil
		if useEnvironment {
			t.Setenv("BETTERZIP_MCP_ROOT", authorizationDir(t))
		} else {
			a.override = new(authorizationDir(t))
		}
		result, err := tool.Handler(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{}}})
		if err != nil || !result.IsError || calls != 0 {
			t.Fatal("configuration silently replaced an explicit override")
		}
	}
}

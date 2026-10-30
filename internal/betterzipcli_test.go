//go:build integration

package betterzip

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func prepareArchiveOperation(t *testing.T, operation string) (*Service, func(context.Context) (string, error), string) {
	t.Helper()
	s := testService(t, testDir(t))
	mustWrite(t, filepath.Join(s.RootPath(), "work", "source.txt"), []byte("original data\n"))
	if operation == "compress" {
		return s, func(ctx context.Context) (string, error) {
			return s.Compress(ctx, "work", "result.zip", []string{"source.txt"})
		}, "work/result.zip"
	}
	if _, err := s.Compress(t.Context(), "work", "seed.zip", []string{"source.txt"}); err != nil {
		t.Fatal(err)
	}
	return s, func(ctx context.Context) (string, error) {
		return s.Extract(ctx, "work/seed.zip", "work/out")
	}, "work/out"
}

func assertArchiveOutput(t *testing.T, s *Service, output string) {
	t.Helper()
	path := filepath.Join(s.RootPath(), output)
	var data []byte
	if strings.HasSuffix(output, ".zip") {
		archive, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := archive.Close(); err != nil {
				t.Error(err)
			}
		}()
		if len(archive.File) != 1 || archive.File[0].Name != "source.txt" {
			t.Fatal("committed archive has unexpected entries")
		}
		file, err := archive.File[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(file)
		if err := errors.Join(err, file.Close()); err != nil {
			t.Fatal(err)
		}
	} else {
		var err error
		data, err = os.ReadFile(filepath.Join(path, "source.txt"))
		if err != nil {
			t.Fatal(err)
		}
	}
	if string(data) != "original data\n" {
		t.Fatalf("committed output changed: %q", data)
	}
	source, err := os.ReadFile(filepath.Join(s.RootPath(), "work", "source.txt"))
	if err != nil || string(source) != "original data\n" {
		t.Fatalf("source changed: %q, %v", source, err)
	}
}

func TestOperationCommittedCleanupFailure(t *testing.T) {
	for _, operation := range []string{"compress", "extract"} {
		for _, failurePoint := range []string{"close", "remove"} {
			t.Run(operation+"/"+failurePoint, func(t *testing.T) {
				s, invoke, output := prepareArchiveOperation(t, operation)
				cleanupErr := errors.New("injected cleanup failure")
				if failurePoint == "close" {
					s.cleanup.closeResource = func(resource io.Closer) error {
						err := resource.Close()
						if root, ok := resource.(*os.Root); ok && strings.HasPrefix(filepath.Base(root.Name()), ".betterzip-mcp-") {
							return errors.Join(err, cleanupErr)
						}
						return err
					}
				} else {
					s.cleanup.removeTree = func(parent *os.Root, name string) error {
						return cleanupErr
					}
				}
				path, err := invoke(t.Context())
				failure, ok := errors.AsType[*operationError](err)
				if !errors.Is(err, cleanupErr) || !ok || !failure.result.committed || failure.result.outputPath != output {
					t.Fatalf("lost committed result or cause: %v", err)
				}
				if path != filepath.Join(s.RootPath(), output) {
					t.Fatal("legacy API lost the output path")
				}
				wantResiduals := 0
				if failurePoint == "remove" {
					wantResiduals = 1
				}
				if len(failure.result.residualPaths) != wantResiduals {
					t.Fatalf("residuals = %v", failure.result.residualPaths)
				}
				for _, residual := range failure.result.residualPaths {
					if !filepath.IsLocal(residual) {
						t.Fatal("residual is not root-relative")
					}
					if _, err := s.root.Stat(residual); err != nil {
						t.Fatal(err)
					}
				}
				assertArchiveOutput(t, s, output)
				if path, err := invoke(t.Context()); path != "" || !errors.Is(err, os.ErrExist) {
					t.Fatalf("repeat must reject the existing output: %v", err)
				}
				assertArchiveOutput(t, s, output)
			})
		}
	}
}

func TestExtractionCloseFailureBeforeCommit(t *testing.T) {
	for _, resourceKind := range []string{"file", "directory"} {
		t.Run(resourceKind, func(t *testing.T) {
			s, invoke, output := prepareArchiveOperation(t, "extract")
			closeErr := errors.New("injected output close failure")
			closed := 0
			s.cleanup.closeResource = func(resource io.Closer) error {
				err := resource.Close()
				match := false
				switch resource := resource.(type) {
				case *os.File:
					match = resourceKind == "file" && filepath.Base(resource.Name()) == "source.txt"
				case *os.Root:
					match = resourceKind == "directory" && filepath.Base(resource.Name()) == "out"
				}
				if match {
					closed++
					return errors.Join(err, closeErr)
				}
				return err
			}
			if path, err := invoke(t.Context()); path != "" || !errors.Is(err, closeErr) {
				t.Fatalf("close failure must prevent commit: %v", err)
			}
			if closed != 1 {
				t.Fatalf("output resource closed %d times", closed)
			}
			if _, err := s.root.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("uncommitted output remains: %v", err)
			}
			noScratch(t, filepath.Join(s.RootPath(), "work"))
		})
	}
}

func TestOperationRollbackRetainsAllErrors(t *testing.T) {
	s, invoke, output := prepareArchiveOperation(t, "extract")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.config.SevenZip = executable // Existing fixture emits a truncated second entry.
	closeErr := errors.New("injected output close failure")
	removeErr := &os.PathError{Op: "remove", Path: "out", Err: os.ErrPermission}
	s.cleanup.closeResource = func(resource io.Closer) error {
		err := resource.Close()
		if root, ok := resource.(*os.Root); ok && filepath.Base(root.Name()) == "out" {
			return errors.Join(err, closeErr)
		}
		return err
	}
	s.cleanup.removeTree = func(parent *os.Root, name string) error { return removeErr }
	path, err := invoke(t.Context())
	failure, ok := errors.AsType[*operationError](err)
	if path != "" || !ok || failure.result.committed || failure.result.outputPath != "" {
		t.Fatalf("incomplete data reported as committed: %v", err)
	}
	for _, cause := range []error{ErrUnsafe, closeErr, os.ErrPermission} {
		if !errors.Is(err, cause) {
			t.Fatalf("lost error cause: %v", cause)
		}
	}
	if pathErr, ok := errors.AsType[*os.PathError](err); !ok || pathErr != removeErr {
		t.Fatal("lost typed cleanup error")
	}
	if len(failure.result.residualPaths) != 2 || !slices.Contains(failure.result.residualPaths, output) {
		t.Fatalf("missing residual paths: %v", failure.result.residualPaths)
	}
	for _, residual := range failure.result.residualPaths {
		if !filepath.IsLocal(residual) {
			t.Fatal("residual is not root-relative")
		}
		if _, err := s.root.Stat(residual); err != nil {
			t.Fatal(err)
		}
	}
	message := toolError(err).Content[0].(mcp.TextContent).Text
	if !strings.Contains(message, "本次未完成提交") || !strings.Contains(message, output) || strings.Contains(message, "产物已完成") {
		t.Fatalf("misleading incomplete-result message: %s", message)
	}
}

func TestOperationCancellationAtCommitBoundary(t *testing.T) {
	for _, operation := range []string{"compress", "extract"} {
		t.Run(operation+"/after", func(t *testing.T) {
			s, invoke, output := prepareArchiveOperation(t, operation)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s.cleanup.removeTree = func(parent *os.Root, name string) error {
				err := parent.RemoveAll(name)
				cancel()
				return err
			}
			path, err := invoke(ctx)
			if !errors.Is(ctx.Err(), context.Canceled) || err != nil || path != filepath.Join(s.RootPath(), output) {
				t.Fatalf("late cancellation changed the committed result: %v", err)
			}
			assertArchiveOutput(t, s, output)
		})
	}
	t.Run("extract/before", func(t *testing.T) {
		s, invoke, output := prepareArchiveOperation(t, "extract")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		s.cleanup.closeResource = func(resource io.Closer) error {
			err := resource.Close()
			if root, ok := resource.(*os.Root); ok && filepath.Base(root.Name()) == "out" {
				cancel()
			}
			return err
		}
		if path, err := invoke(ctx); path != "" || !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-commit cancellation was ignored: %v", err)
		}
		if _, err := s.root.Stat(output); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancelled output remains: %v", err)
		}
		noScratch(t, filepath.Join(s.RootPath(), "work"))
	})
}

func TestMCPOperationResultCompatibility(t *testing.T) {
	for _, operation := range []string{"compress", "extract"} {
		for _, cleanupFailure := range []bool{false, true} {
			name := operation + "/success"
			if cleanupFailure {
				name = operation + "/cleanup-failure"
			}
			t.Run(name, func(t *testing.T) {
				s, _, output := prepareArchiveOperation(t, operation)
				if cleanupFailure {
					s.cleanup.removeTree = func(parent *os.Root, name string) error {
						return errors.New("injected cleanup failure")
					}
				}
				args := map[string]any{"working_dir": "work", "output_archive": "result.zip", "sources": []string{"source.txt"}}
				prefix := "压缩完成："
				if operation == "extract" {
					args = map[string]any{"archive_path": "work/seed.zip", "output_dir": "work/out"}
					prefix = "解压完成："
				}
				server := NewMCPServer(s)
				tool := server.GetTool("betterzip_" + operation)
				result, err := tool.Handler(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
				if err != nil || result == nil {
					t.Fatalf("tool invocation failed: %v", err)
				}
				if len(server.ListTools()) != 4 || result.IsError != cleanupFailure || result.StructuredContent != nil || len(result.Content) != 1 {
					t.Fatalf("legacy result shape changed: %+v", result)
				}
				message := result.Content[0].(mcp.TextContent).Text
				if cleanupFailure {
					if !strings.Contains(message, "产物已完成") || !strings.Contains(message, output) || !strings.Contains(message, "请勿直接重试") || strings.Contains(message, s.RootPath()) {
						t.Fatalf("committed output omitted or unredacted: %s", message)
					}
				} else if message != prefix+filepath.Join(s.RootPath(), output) {
					t.Fatalf("success text changed: %s", message)
				}
				assertArchiveOutput(t, s, output)
			})
		}
	}
}

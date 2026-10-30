//go:build integration

package betterzip

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestMain also serves as a deterministic child-process fixture. It never
// replaces the real 7za used by the round-trip and malicious-archive tests.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--internal-sevenzip":
			if err := ExecSevenZip(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(92)
			}
		case "helper-block", "helper-child":
			if os.Args[1] == "helper-block" {
				child := exec.Command(os.Args[0], "helper-child")
				if err := child.Start(); err != nil {
					os.Exit(91)
				}
				fmt.Printf("%d %d\n", os.Getpid(), child.Process.Pid)
			}
			time.Sleep(time.Hour)
			os.Exit(0)
		case "helper-exit":
			fmt.Fprint(os.Stderr, "controlled failure")
			os.Exit(7)
		case "helper-flood":
			fmt.Print(strings.Repeat("x", 4096))
			os.Exit(0)
		case "l":
			fmt.Print("Path = first\nSize = 4\nFolder = -\n\nPath = second\nSize = 4\nFolder = -\n\n")
			os.Exit(0)
		case "x":
			if os.Args[len(os.Args)-1] == "first" {
				fmt.Print("full")
			} else {
				fmt.Print("bad")
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func mustWrite(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func noScratch(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".betterzip-mcp-") {
			t.Errorf("scratch data left behind: %s", entry.Name())
		}
	}
}

func TestRealArchiveRoundTrip(t *testing.T) {
	if _, err := FindSevenZip(""); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"zip", "7z"} {
		t.Run(format, func(t *testing.T) {
			root := testDir(t)
			s := testService(t, root)
			files := map[string][]byte{
				"notes.txt":     []byte("archive round trip\n"),
				"nested/报告.txt": []byte("中文内容\n"),
				"-switch":       []byte("literal switch"),
				"@listfile":     []byte("literal listfile"),
				"literal*.txt":  []byte("literal wildcard"),
				"empty-file":    {},
			}
			for name, data := range files {
				mustWrite(t, filepath.Join(root, name), data)
			}
			if err := os.Mkdir(filepath.Join(root, "empty-dir"), 0700); err != nil {
				t.Fatal(err)
			}
			archive, err := s.Compress(t.Context(), root, "bundle."+format, []string{"."})
			if err != nil {
				t.Fatal(err)
			}
			out, err := s.Extract(t.Context(), archive, "out")
			if err != nil {
				helper, _ := FindSevenZip("")
				listing, listingErr := exec.CommandContext(t.Context(), helper, "l", "-slt", "-ba", archive).CombinedOutput()
				t.Logf("actual listing (%v):\n%s", listingErr, listing)
				t.Fatal(err)
			}
			for name, expected := range files {
				data, err := os.ReadFile(filepath.Join(out, name))
				if err != nil || !bytes.Equal(data, expected) {
					t.Errorf("%s: got %q, err %v", name, data, err)
				}
			}
			if info, err := os.Stat(filepath.Join(out, "empty-dir")); err != nil || !info.IsDir() {
				t.Fatalf("empty directory: %v", err)
			}
			if _, err := s.Compress(t.Context(), root, "bundle."+format, []string{"notes.txt"}); !errors.Is(err, os.ErrExist) {
				t.Fatalf("overwrote archive: %v", err)
			}
			if _, err := s.Extract(t.Context(), archive, "out"); !errors.Is(err, os.ErrExist) {
				t.Fatalf("overwrote directory: %v", err)
			}
			noScratch(t, root)
		})
	}
}

func makeZIP(t *testing.T, archive, entry string, mode os.FileMode, data []byte) {
	t.Helper()
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	header := &zip.FileHeader{Name: entry, Method: zip.Deflate}
	header.SetMode(mode)
	entryWriter, err := w.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entryWriter.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRealUnsafeArchivesAndLimits(t *testing.T) {
	for _, name := range []string{"../escaped", "/absolute", `back\slash`, "link"} {
		t.Run(name, func(t *testing.T) {
			root := testDir(t)
			s := testService(t, root)
			mode := os.FileMode(0600)
			if name == "link" {
				mode |= os.ModeSymlink
			}
			makeZIP(t, filepath.Join(root, "bad.zip"), name, mode, []byte("../outside"))
			if _, err := s.Extract(t.Context(), "bad.zip", "out"); !errors.Is(err, ErrUnsafe) {
				t.Fatalf("unsafe archive accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "out")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected output: %v", err)
			}
			noScratch(t, root)
		})
	}
	t.Run("expanded-size", func(t *testing.T) {
		root := testDir(t)
		s := testService(t, root)
		s.config.MaxBytes = 512
		makeZIP(t, filepath.Join(root, "bomb.zip"), "big", 0600, bytes.Repeat([]byte{0}, 4096))
		if _, err := s.Extract(t.Context(), "bomb.zip", "out"); !errors.Is(err, ErrLimit) {
			t.Fatalf("expanded limit: %v", err)
		}
		noScratch(t, root)
	})
	t.Run("source-size", func(t *testing.T) {
		root := testDir(t)
		s := testService(t, root)
		s.config.MaxBytes = 8
		mustWrite(t, filepath.Join(root, "big"), []byte("123456789"))
		if _, err := s.Compress(t.Context(), root, "out.zip", []string{"big"}); !errors.Is(err, ErrLimit) {
			t.Fatalf("source limit: %v", err)
		}
		noScratch(t, root)
	})
}

type readyWriter struct {
	bytes.Buffer
	once  sync.Once
	ready chan struct{}
}

func (w *readyWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if bytes.Contains(w.Bytes(), []byte{'\n'}) {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

func TestProcessCancellationExitAndOutputLimit(t *testing.T) {
	root := testDir(t)
	s := testService(t, root)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.config.SevenZip = executable
	dir, err := s.root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := dir.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Run("cancel-process-group", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		writer := &readyWriter{ready: make(chan struct{})}
		done := make(chan error, 1)
		go func() { _, err := s.run(ctx, dir, writer, 1024, "helper-block"); done <- err }()
		select {
		case <-writer.ready:
		case err := <-done:
			t.Fatalf("helper failed to start: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("helper startup timeout")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancel did not reap child")
		}
		for field := range strings.FieldsSeq(writer.String()) {
			pid, err := strconv.Atoi(field)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("process %d is still present", pid)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	})
	t.Run("exit-code", func(t *testing.T) {
		_, err := s.run(t.Context(), dir, io.Discard, 1024, "helper-exit")
		commandError, ok := errors.AsType[*CommandError](err)
		if !ok || commandError.Code != 7 || commandError.Detail != "controlled failure" {
			t.Fatalf("exit result: %v", err)
		}
	})
	t.Run("stdout-limit", func(t *testing.T) {
		if _, err := s.run(t.Context(), dir, io.Discard, 8, "helper-flood"); !errors.Is(err, ErrLimit) {
			t.Fatalf("limit result: %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		if _, err := s.run(ctx, dir, io.Discard, 1024, "helper-child"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout result: %v", err)
		}
	})
}

func TestPartialExtractionIsRemoved(t *testing.T) {
	root := testDir(t)
	s := testService(t, root)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.config.SevenZip = executable
	mustWrite(t, filepath.Join(root, "fixture.zip"), []byte("controlled process fixture"))
	if _, err := s.Extract(t.Context(), "fixture.zip", "out"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("partial extraction: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "out")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial output was retained: %v", err)
	}
	noScratch(t, root)
}

func TestFIFORejectedWithoutBlocking(t *testing.T) {
	root := testDir(t)
	s := testService(t, root)
	if err := syscall.Mkfifo(filepath.Join(root, "fifo.zip"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Extract(t.Context(), "fifo.zip", "out"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("fifo: %v", err)
	}
	noScratch(t, root)
}

func TestRealEncryptedArchivesAreRejected(t *testing.T) {
	for _, format := range []string{"zip", "7z"} {
		t.Run(format, func(t *testing.T) {
			root := testDir(t)
			s := testService(t, root)
			mustWrite(t, filepath.Join(root, "secret.txt"), []byte("fixture secret"))
			dir, err := s.root.Open(".")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := dir.Close(); err != nil {
					t.Error(err)
				}
			})
			args := []string{"a", "-t" + format, "-pfixture-password", "-y", "-bd", "--", "encrypted." + format, "secret.txt"}
			if _, err := s.run(t.Context(), dir, io.Discard, 4<<20, args...); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Extract(t.Context(), "encrypted."+format, "out"); !errors.Is(err, ErrUnsafe) {
				t.Fatalf("encrypted entry accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "out")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("encrypted output: %v", err)
			}
			noScratch(t, root)
		})
	}
}

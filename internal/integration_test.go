//go:build integration

package betterzip

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func noScratch(t testing.TB, root string) {
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

type extractBenchmarkCase struct {
	format string
	files  int
	size   int64
}

func (c extractBenchmarkCase) name() string {
	return fmt.Sprintf("%s/files=%d/bytes=%d", c.format, c.files, c.size)
}

type extractBenchmarkFixture struct {
	service       *Service
	archive       string
	contentHash   string
	archiveHash   string
	archiveBytes  int64
	helperVersion string
	options       []string
}

func benchmarkHash(b testing.TB, filename string) string {
	b.Helper()
	file, err := os.Open(filename)
	if err != nil {
		b.Fatal(err)
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		b.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func makeExtractBenchmarkFixture(b *testing.B, c extractBenchmarkCase) extractBenchmarkFixture {
	b.Helper()
	s := testService(b, testDir(b))
	if err := s.root.Mkdir("src", 0700); err != nil {
		b.Fatal(err)
	}
	block := make([]byte, 32<<10)
	random := rand.New(rand.NewPCG(1, 2))
	for i := range block {
		block[i] = byte(random.Uint32())
	}
	contentHash := ""
	for i := range c.files {
		name := filepath.Join("src", fmt.Sprintf("file-%05d.bin", i))
		file, err := s.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			b.Fatal(err)
		}
		for remaining := c.size; remaining > 0; {
			n, writeErr := file.Write(block[:min(int64(len(block)), remaining)])
			if writeErr != nil {
				b.Fatal(errors.Join(writeErr, file.Close()))
			}
			remaining -= int64(n)
		}
		if err := file.Close(); err != nil {
			b.Fatal(err)
		}
		stamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(filepath.Join(s.RootPath(), name), stamp, stamp); err != nil {
			b.Fatal(err)
		}
		if i == 0 {
			contentHash = benchmarkHash(b, filepath.Join(s.RootPath(), name))
		}
	}
	format := "7z"
	if c.format == "zip" {
		format = "zip"
	}
	archive := "input." + format
	options := []string{"a", "-t" + format, "-mx=1", "-y", "-bd", "-bb0", "-sccUTF-8", "-spd", "-sse", "-w.."}
	if c.format == "7z-solid" {
		options = append(options, "-ms=on")
	} else if c.format == "7z-nonsolid" {
		options = append(options, "-ms=off")
	}
	options = append(options, "--", "../"+archive, ".")
	dir, err := s.root.Open("src")
	if err != nil {
		b.Fatal(err)
	}
	_, runErr := s.run(b.Context(), dir, io.Discard, 4<<20, options...)
	if err := errors.Join(runErr, dir.Close()); err != nil {
		b.Fatal(err)
	}
	info, err := s.root.Stat(archive)
	if err != nil {
		b.Fatal(err)
	}
	helper, err := FindSevenZip("")
	if err != nil {
		b.Fatal(err)
	}
	help, err := exec.CommandContext(b.Context(), helper, "--help").Output()
	if err != nil {
		b.Fatal(err)
	}
	version := ""
	for line := range strings.Lines(string(help)) {
		if strings.Contains(line, "7-Zip") {
			version = strings.TrimSpace(line)
			break
		}
	}
	return extractBenchmarkFixture{s, archive, contentHash, benchmarkHash(b, filepath.Join(s.RootPath(), archive)), info.Size(), version, options}
}

func checkExtractBenchmarkOutput(b testing.TB, fixture extractBenchmarkFixture, c extractBenchmarkCase, output string) {
	b.Helper()
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != c.files {
		b.Fatalf("output entry count: %d, %v", len(entries), err)
	}
	for i := range c.files {
		name := filepath.Join(output, fmt.Sprintf("file-%05d.bin", i))
		info, err := os.Stat(name)
		if err != nil || info.Size() != c.size {
			b.Fatalf("output size mismatch: %v", err)
		}
		if benchmarkHash(b, name) != fixture.contentHash {
			b.Fatal("output content hash mismatch")
		}
	}
}

func recordExtractBenchmark(b testing.TB, record map[string]any) {
	b.Helper()
	filename := os.Getenv("BETTERZIP_MCP_BENCH_REPORT")
	if filename == "" {
		return
	}
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		b.Fatal(err)
	}
	err = json.NewEncoder(file).Encode(record)
	if err := errors.Join(err, file.Close()); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkExtract(b *testing.B) {
	for _, format := range []string{"zip", "7z-nonsolid", "7z-solid"} {
		cases := []extractBenchmarkCase{{format, 1, 1024}, {format, 100, 1024}, {format, 1000, 1024}, {format, 10000, 1024}, {format, 1, 1 << 20}, {format, 1, 64 << 20}, {format, 1, 256 << 20}}
		for _, c := range cases {
			b.Run(c.name(), func(b *testing.B) {
				fixture := makeExtractBenchmarkFixture(b, c)
				mode := os.Getenv("BETTERZIP_MCP_BENCH_LABEL")
				recordExtractBenchmark(b, map[string]any{"kind": "fixture", "label": mode, "case": c.name(), "files": c.files, "file_bytes": c.size, "content_sha256": fixture.contentHash, "archive_sha256": fixture.archiveHash, "archive_bytes": fixture.archiveBytes, "compression_args": fixture.options, "go_version": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0), "helper_version": fixture.helperVersion})
				// The warm-up, verification and deletion are outside the timed operation.
				output, err := fixture.service.Extract(b.Context(), fixture.archive, "out")
				if err != nil {
					b.Fatal(err)
				}
				checkExtractBenchmarkOutput(b, fixture, c, output)
				if err := fixture.service.root.RemoveAll("out"); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.SetBytes(int64(c.files) * c.size)
				sample := 0
				for b.Loop() {
					start := time.Now()
					output, err := fixture.service.Extract(b.Context(), fixture.archive, "out")
					elapsed := time.Since(start)
					b.StopTimer()
					if err != nil {
						b.Fatal(err)
					}
					checkExtractBenchmarkOutput(b, fixture, c, output)
					noScratch(b, fixture.service.RootPath())
					if err := fixture.service.root.RemoveAll("out"); err != nil {
						b.Fatal(err)
					}
					sample++
					recordExtractBenchmark(b, map[string]any{"kind": "sample", "label": mode, "case": c.name(), "sample": sample, "duration_ns": elapsed.Nanoseconds(), "expected_helper_calls": c.files + 1, "verified": true})
					fmt.Printf("MEASURE %s sample=%d elapsed=%s\n", c.name(), sample, elapsed)
					b.StartTimer()
				}
			})
		}
	}
}

func BenchmarkExtractPreparation(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := FindSevenZip(""); err != nil {
			b.Fatal(err)
		}
		if _, err := os.Executable(); err != nil {
			b.Fatal(err)
		}
	}
}

// Stage probes reuse the same I/O and helper calls, but are diagnostic rather
// than a replacement for the end-to-end Extract measurement above.
func measureExtractStages(b *testing.B, fixture extractBenchmarkFixture, c extractBenchmarkCase) (stages map[string]int64) {
	b.Helper()
	s := fixture.service
	ctx, cancel := context.WithTimeout(b.Context(), s.config.Timeout)
	defer cancel()
	name, job, dir, err := newJob(s.root)
	if err != nil {
		b.Fatal(err)
	}
	stages = make(map[string]int64)
	defer func() {
		start := time.Now()
		closeErr, removeErr := closeJob(s.root, name, job, dir, cleanupActions{})
		stages["cleanup_ns"] = time.Since(start).Nanoseconds()
		if err := errors.Join(closeErr, removeErr); err != nil {
			b.Error(err)
		}
	}()
	start := time.Now()
	if _, err := copyFile(ctx, s.root, job, fixture.archive, fixture.archive, s.config.MaxBytes); err != nil {
		b.Fatal(err)
	}
	stages["copy_ns"] = time.Since(start).Nanoseconds()
	format, err := archiveFormat(fixture.archive)
	if err != nil {
		b.Fatal(err)
	}
	var listing bytes.Buffer
	start = time.Now()
	if _, err := s.run(ctx, dir, &listing, 4<<20, "l", "-t"+format, "-slt", "-ba", "-bd", "-sccUTF-8", "--", fixture.archive); err != nil {
		b.Fatal(err)
	}
	stages["list_ns"] = time.Since(start).Nanoseconds()
	start = time.Now()
	entries, err := parseListing(listing.String(), s.config.MaxBytes)
	if err != nil {
		b.Fatal(err)
	}
	stages["parse_ns"] = time.Since(start).Nanoseconds()
	if err := job.Mkdir("out", 0700); err != nil {
		b.Fatal(err)
	}
	out, err := job.OpenRoot("out")
	if err != nil {
		b.Fatal(err)
	}
	outClosed := false
	defer func() {
		if !outClosed {
			if err := out.Close(); err != nil {
				b.Error(err)
			}
		}
	}()
	start = time.Now()
	for _, entry := range entries {
		if entry.dir {
			b.Fatal("unexpected directory in benchmark fixture")
		}
		file, err := out.OpenFile(entry.name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			b.Fatal(err)
		}
		n, runErr := s.run(ctx, dir, file, entry.size, "x", "-t"+format, "-so", "-y", "-bd", "-bb0", "-sccUTF-8", "-spd", "--", fixture.archive, entry.name)
		if err := errors.Join(runErr, file.Close()); err != nil {
			b.Fatal(err)
		}
		if n != entry.size {
			b.Fatal("stage probe output size mismatch")
		}
		stages["helper_calls"]++
	}
	err = out.Close()
	outClosed = true
	if err != nil {
		b.Fatal(err)
	}
	stages["extract_ns"] = time.Since(start).Nanoseconds()
	stages["helper_calls"]++ // The listing command.
	checkExtractBenchmarkOutput(b, fixture, c, filepath.Join(s.RootPath(), name, "out"))
	return stages
}

func BenchmarkExtractStages(b *testing.B) {
	for _, format := range []string{"zip", "7z-nonsolid", "7z-solid"} {
		for _, c := range []extractBenchmarkCase{{format, 1000, 1024}, {format, 1, 64 << 20}} {
			b.Run(c.name(), func(b *testing.B) {
				fixture := makeExtractBenchmarkFixture(b, c)
				for b.Loop() {
					stages := measureExtractStages(b, fixture, c)
					b.StopTimer()
					recordExtractBenchmark(b, map[string]any{"kind": "stages", "case": c.name(), "times": stages})
					fmt.Printf("STAGES %s %v\n", c.name(), stages)
					b.StartTimer()
				}
			})
		}
	}
}

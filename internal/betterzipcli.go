package betterzip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	Version               = "0.1.1"
	DefaultTimeout        = 5 * time.Minute
	DefaultMaxBytes int64 = 4 << 30
)

// Config is trusted startup configuration, never supplied by an MCP tool call.
type Config struct {
	Root     string
	SevenZip string
	Timeout  time.Duration
	MaxBytes int64
}

// Service owns one authorized filesystem root. Close after the MCP transport stops.
type Service struct {
	root     *os.Root
	rootPath string
	config   Config
	cleanup  cleanupActions
}

// Cleanup overrides belong to one service, never to package-wide mutable state.
// Nil functions use the operating system directly.
type cleanupActions struct {
	closeResource func(io.Closer) error
	removeTree    func(*os.Root, string) error
}

func (c cleanupActions) close(resource io.Closer) error {
	if c.closeResource != nil {
		return c.closeResource(resource)
	}
	return resource.Close()
}

func (c cleanupActions) removeAll(parent *os.Root, name string) error {
	if c.removeTree != nil {
		return c.removeTree(parent, name)
	}
	return parent.RemoveAll(name)
}

// operationResult tracks publication independently of later cleanup failures.
// Its paths are relative to the authorized root; public success paths stay intact.
type operationResult struct {
	committed     bool
	outputPath    string
	cleanupErr    error
	residualPaths []string
}

type operationError struct {
	result operationResult
	cause  error
}

func (e *operationError) Error() string { return e.cause.Error() }
func (e *operationError) Unwrap() error { return e.cause }

func (r *operationResult) addCleanup(err error) {
	if err != nil {
		r.cleanupErr = errors.Join(r.cleanupErr, err)
	}
}

func (r *operationResult) finish(err *error) {
	if r.cleanupErr != nil {
		*err = &operationError{result: *r, cause: errors.Join(*err, r.cleanupErr)}
	}
}

func (r *operationResult) removeAll(parent *os.Root, name, relativePath string, cleanup cleanupActions) {
	if err := cleanup.removeAll(parent, name); err != nil {
		r.addCleanup(err)
		r.residualPaths = append(r.residualPaths, relativePath)
	}
}

func (r *operationResult) closeJob(parent *os.Root, name string, root *os.Root, dir *os.File, relativePath string, cleanup cleanupActions) {
	closeErr, removeErr := closeJob(parent, name, root, dir, cleanup)
	r.addCleanup(closeErr)
	if removeErr != nil {
		r.addCleanup(removeErr)
		r.residualPaths = append(r.residualPaths, relativePath)
	}
}

func New(config Config) (*Service, error) {
	if config.Timeout <= 0 || config.MaxBytes <= 0 || config.MaxBytes > 1<<50 {
		return nil, fmt.Errorf("%w: timeout and max-bytes must be positive; max-bytes <= 1 PiB", ErrInvalid)
	}
	path, err := expandPath(config.Root)
	if err != nil {
		return nil, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve authorized root: %w", err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return &Service{root: root, rootPath: path, config: config}, nil
}

func (s *Service) Close() error     { return s.root.Close() }
func (s *Service) RootPath() string { return s.rootPath }

// FindSevenZip only discovers the bundled helper or an explicit trusted override.
func FindSevenZip(override string) (string, error) {
	var candidates []string
	if override != "" {
		path, err := expandPath(override)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(path) {
			return "", fmt.Errorf("%w: --sevenzip must be an absolute path", ErrInvalid)
		}
		candidates = []string{path}
	} else {
		candidates = []string{"/Applications/BetterZip.app/Contents/Helpers/7za"}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, "Applications/BetterZip.app/Contents/Helpers/7za"))
		}
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", errors.New("未找到 BetterZip 随附的 7za；请安装 BetterZip，或使用 --sevenzip 指定其 Helpers/7za 的绝对路径")
}

// CommandError retains both the operating-system error and the actual exit code.
type CommandError struct {
	Code   int
	Detail string
	Cause  error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("7za 执行失败（退出码 %d）：%s", e.Code, e.Detail)
}
func (e *CommandError) Unwrap() error { return e.Cause }

type boundedWriter struct {
	w         io.Writer
	remaining int64
	written   int64
	err       error
	cancel    context.CancelFunc
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		w.err = ErrLimit
		w.cancel()
		return 0, w.err
	}
	n, err := w.w.Write(p)
	w.remaining -= int64(n)
	w.written += int64(n)
	if err != nil {
		w.err = err
		w.cancel()
	}
	return n, err
}

type diagnosticBuffer struct{ bytes.Buffer }

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := (64 << 10) - b.Len(); room > 0 {
		_, _ = b.Buffer.Write(p[:min(room, len(p))]) // bytes.Buffer.Write cannot fail.
	}
	return n, nil
}

// ExecSevenZip is the private child entrypoint. It changes directory using the
// inherited descriptor, then replaces itself; the parent still controls one PID.
func ExecSevenZip(args []string) error {
	if len(args) == 0 || !filepath.IsAbs(args[0]) {
		return fmt.Errorf("%w: missing internal executable", ErrInvalid)
	}
	dir := os.NewFile(3, "sevenzip-working-directory")
	if dir == nil {
		return errors.New("missing inherited directory descriptor")
	}
	info, err := dir.Stat()
	if err != nil {
		return errors.Join(err, dir.Close())
	}
	if !info.IsDir() {
		return errors.Join(errors.New("inherited descriptor is not a directory"), dir.Close())
	}
	if err := syscall.Fchdir(int(dir.Fd())); err != nil {
		return errors.Join(err, dir.Close())
	}
	if err := dir.Close(); err != nil {
		return err
	}
	return syscall.Exec(args[0], args, os.Environ())
}

// run confines all 7za filesystem arguments to private scratch data. A re-exec
// of this binary passes the working directory as fd 3, avoiding path re-resolution.
func (s *Service) run(ctx context.Context, dir *os.File, output io.Writer, limit int64, args ...string) (int64, error) {
	binary, err := FindSevenZip(s.config.SevenZip)
	if err != nil {
		return 0, err
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	childArgs := append([]string{"--internal-sevenzip", binary}, args...)
	cmd := exec.CommandContext(childCtx, self, childArgs...)
	cmd.ExtraFiles = []*os.File{dir}
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8", "TMPDIR=."}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	var diagnostics diagnosticBuffer
	bounded := &boundedWriter{w: output, remaining: limit, cancel: cancel}
	cmd.Stdout = bounded
	cmd.Stderr = &diagnostics
	err = cmd.Run() // Run always waits/reaps a successfully started child.
	if bounded.err != nil {
		return bounded.written, bounded.err
	}
	if ctx.Err() != nil {
		return bounded.written, ctx.Err()
	}
	if err != nil {
		code := -1
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exitErr.ExitCode()
		}
		detail := strings.TrimSpace(diagnostics.String())
		if detail == "" {
			detail = err.Error()
		}
		return bounded.written, &CommandError{code, detail, err}
	}
	return bounded.written, nil
}

// Compress copies validated inputs to private runtime scratch data, then creates
// one new ZIP or 7z. Existing archives are never updated or overwritten.
func (s *Service) Compress(ctx context.Context, workingDir, output string, sources []string) (path string, err error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	var result operationResult
	defer result.finish(&err)
	cleanup := s.cleanup
	workRel, err := relativePath(s.rootPath, workingDir)
	if err != nil {
		return "", err
	}
	if err := noSymlinks(s.root, workRel); err != nil {
		return "", err
	}
	work, err := s.root.OpenRoot(workRel)
	if err != nil {
		return "", err
	}
	defer func() { result.addCleanup(cleanup.close(work)) }()
	name, err := archiveName(output)
	if err != nil || filepath.Base(output) != output || name != output {
		return "", fmt.Errorf("%w: output_archive must be a filename", ErrInvalid)
	}
	format, err := archiveFormat(output)
	if err != nil {
		return "", err
	}
	if _, err := work.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return "", fmt.Errorf("output archive: %w", err)
	}
	workPath := filepath.Join(s.rootPath, workRel)
	entries, err := collectSources(ctx, work, workPath, sources)
	if err != nil {
		return "", err
	}
	jobName, job, dir, err := newJob(work)
	if err != nil {
		return "", err
	}
	defer result.closeJob(work, jobName, job, dir, filepath.Join(workRel, jobName), cleanup)
	if err := job.Mkdir("input", 0700); err != nil {
		return "", err
	}
	input, err := job.OpenRoot("input")
	if err != nil {
		return "", err
	}
	defer func() { result.addCleanup(cleanup.close(input)) }()
	var copied int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if entry.dir {
			if err := input.MkdirAll(entry.path, 0700); err != nil {
				return "", err
			}
			continue
		}
		if err := input.MkdirAll(filepath.Dir(entry.path), 0700); err != nil {
			return "", err
		}
		n, err := copyFile(ctx, work, input, entry.path, entry.path, s.config.MaxBytes-copied)
		if err != nil {
			return "", err
		}
		copied += n
	}
	inputDir, err := input.Open(".")
	if err != nil {
		return "", err
	}
	defer func() { result.addCleanup(cleanup.close(inputDir)) }()
	// The output is a sibling of input; both are private, generated names.
	_, err = s.run(ctx, inputDir, io.Discard, 4<<20, "a", "-t"+format, "-y", "-bd", "-bb0", "-sccUTF-8", "-spd", "-sse", "-w..", "--", "../result."+format, ".")
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// link(2) atomically refuses an existing destination, even across a race.
	if err := work.Link(filepath.Join(jobName, "result."+format), output); err != nil {
		return "", err
	}
	result.committed = true
	result.outputPath = filepath.Join(workRel, output)
	return filepath.Join(workPath, output), nil
}

// Extract streams each vetted entry through stdout and lets os.Root perform all
// output writes. The caller must provide a new output directory.
func (s *Service) Extract(ctx context.Context, archivePath, outputDir string) (path string, err error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	var result operationResult
	defer result.finish(&err)
	cleanup := s.cleanup
	archiveRel, err := relativePath(s.rootPath, archivePath)
	if err != nil {
		return "", err
	}
	format, err := archiveFormat(archiveRel)
	if err != nil {
		return "", err
	}
	outputRel, err := relativePath(s.rootPath, outputDir)
	if err != nil || outputRel == "." {
		return "", fmt.Errorf("%w: output_dir must be a new directory inside root", ErrInvalid)
	}
	parentRel, base := filepath.Dir(outputRel), filepath.Base(outputRel)
	if err := noSymlinks(s.root, parentRel); err != nil {
		return "", err
	}
	parent, err := s.root.OpenRoot(parentRel)
	if err != nil {
		return "", err
	}
	defer func() { result.addCleanup(cleanup.close(parent)) }()
	if _, err := parent.Lstat(base); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return "", fmt.Errorf("output directory: %w", err)
	}
	jobName, job, dir, err := newJob(parent)
	if err != nil {
		return "", err
	}
	defer result.closeJob(parent, jobName, job, dir, filepath.Join(parentRel, jobName), cleanup)
	archive := "archive." + format
	if _, err := copyFile(ctx, s.root, job, archiveRel, archive, s.config.MaxBytes); err != nil {
		return "", err
	}
	var listing bytes.Buffer
	_, err = s.run(ctx, dir, &listing, 4<<20, "l", "-t"+format, "-slt", "-ba", "-bd", "-sccUTF-8", "--", archive)
	if err != nil {
		return "", err
	}
	entries, err := parseListing(listing.String(), s.config.MaxBytes)
	if err != nil {
		return "", err
	}
	if err := parent.Mkdir(base, 0700); err != nil {
		return "", err
	}
	defer func() {
		if !result.committed {
			result.removeAll(parent, base, outputRel, cleanup)
		}
	}()
	out, err := parent.OpenRoot(base)
	if err != nil {
		return "", err
	}
	outClosed := false
	defer func() {
		if !outClosed {
			err = errors.Join(err, cleanup.close(out))
		}
	}()
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if entry.dir {
			if err := out.MkdirAll(entry.name, 0700); err != nil {
				return "", err
			}
			continue
		}
		if err := out.MkdirAll(filepath.Dir(entry.name), 0700); err != nil {
			return "", err
		}
		file, err := out.OpenFile(entry.name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", err
		}
		n, runErr := s.run(ctx, dir, file, entry.size, "x", "-t"+format, "-so", "-y", "-bd", "-bb0", "-sccUTF-8", "-spd", "--", archive, entry.name)
		closeErr := cleanup.close(file)
		if err := errors.Join(runErr, closeErr); err != nil {
			return "", err
		}
		if n != entry.size {
			return "", fmt.Errorf("%w: extracted size differs from listing", ErrUnsafe)
		}
	}
	closeErr := cleanup.close(out)
	outClosed = true
	if closeErr != nil {
		return "", closeErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result.committed = true
	result.outputPath = outputRel
	return filepath.Join(s.rootPath, outputRel), nil
}

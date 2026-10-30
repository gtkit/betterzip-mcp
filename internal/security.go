// Package betterzip implements local archiving tools without model or network access.
package betterzip

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"
)

const maxEntries = 10000

var (
	ErrInvalid = errors.New("invalid argument")
	ErrUnsafe  = errors.New("unsafe path or archive")
	ErrLimit   = errors.New("size or entry limit exceeded")
)

// expandPath supports the current user's ~, but never expands environment variables.
func expandPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" || len(value) > 4096 || strings.ContainsAny(value, "\\:") || strings.ContainsFunc(value, unicode.IsControl) {
		return "", fmt.Errorf("%w: empty, oversized or unsupported path", ErrInvalid)
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(value, "~")), nil
	}
	if strings.HasPrefix(value, "~") {
		return "", fmt.Errorf("%w: ~other-user is unsupported", ErrInvalid)
	}
	return filepath.Clean(value), nil
}

func relativePath(base, value string) (string, error) {
	path, err := expandPath(value)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: path must stay inside the authorized directory", ErrUnsafe)
	}
	return rel, nil
}

// noSymlinks rejects each existing path component. os.Root independently enforces
// containment during the later open, including if a component changes meanwhile.
func noSymlinks(root *os.Root, path string) error {
	if !filepath.IsLocal(path) {
		return ErrUnsafe
	}
	current := ""
	for part := range strings.SplitSeq(path, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic links are unsupported", ErrUnsafe)
		}
	}
	return nil
}

func regularFile(root *os.Root, name string) (*os.File, error) {
	if err := noSymlinks(root, name); err != nil {
		return nil, err
	}
	// O_NONBLOCK prevents a malicious FIFO from blocking before its type can be
	// inspected. O_NOFOLLOW also rejects a last-component symlink swap.
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%w: only regular files are supported", ErrUnsafe)
	}
	if err == nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
			err = fmt.Errorf("%w: hard-linked files are unsupported", ErrUnsafe)
		}
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

type sourceEntry struct {
	path string
	dir  bool
}

func collectSources(ctx context.Context, root *os.Root, rootPath string, sources []string) ([]sourceEntry, error) {
	if len(sources) == 0 || len(sources) > maxEntries {
		return nil, fmt.Errorf("%w: sources must contain 1..%d paths", ErrInvalid, maxEntries)
	}
	entries := make([]sourceEntry, 0, len(sources))
	seen := make(map[string]string)
	for _, source := range sources {
		rel, err := relativePath(rootPath, source)
		if err != nil {
			return nil, err
		}
		if err := noSymlinks(root, rel); err != nil {
			return nil, err
		}
		err = fs.WalkDir(root.FS(), filepath.ToSlash(rel), func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if name == "." {
				return nil
			}
			if _, err := archiveName(name); err != nil {
				return err
			}
			if entry.Type()&fs.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
				return fmt.Errorf("%w: links and special files are unsupported", ErrUnsafe)
			}
			key := strings.ToLower(name)
			if previous, ok := seen[key]; ok {
				if previous != name {
					return fmt.Errorf("%w: case-colliding source paths", ErrUnsafe)
				}
				return nil
			}
			if len(entries) == maxEntries {
				return ErrLimit
			}
			seen[key] = name
			entries = append(entries, sourceEntry{path: name, dir: entry.IsDir()})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: no source entries", ErrInvalid)
	}
	return entries, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func copyFile(ctx context.Context, from, to *os.Root, source, destination string, limit int64) (n int64, err error) {
	in, err := regularFile(from, source)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	out, err := to.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(out, contextReader{ctx, io.LimitReader(in, limit+1)})
	closeErr := out.Close()
	if n > limit {
		return n, errors.Join(ErrLimit, copyErr, closeErr)
	}
	return n, errors.Join(copyErr, closeErr)
}

// newJob creates runtime archiving scratch data inside the authorized directory.
func newJob(parent *os.Root) (string, *os.Root, *os.File, error) {
	name := ".betterzip-mcp-" + rand.Text()
	if err := parent.Mkdir(name, 0700); err != nil {
		return "", nil, nil, err
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return "", nil, nil, errors.Join(err, parent.Remove(name))
	}
	dir, err := root.Open(".")
	if err != nil {
		return "", nil, nil, errors.Join(err, root.Close(), parent.Remove(name))
	}
	return name, root, dir, nil
}

// Keep removal errors separate so a failed close does not imply residual data.
func closeJob(parent *os.Root, name string, root *os.Root, dir *os.File, cleanup cleanupActions) (closeErr, removeErr error) {
	return errors.Join(cleanup.close(dir), cleanup.close(root)), cleanup.removeAll(parent, name)
}

func archiveFormat(name string) (string, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".zip":
		return "zip", nil
	case ".7z":
		return "7z", nil
	default:
		return "", fmt.Errorf("%w: supported archive extensions are .zip and .7z", ErrInvalid)
	}
}

func archiveName(name string) (string, error) {
	name = strings.TrimSuffix(name, "/")
	if name == "." || strings.ContainsAny(name, "\\:") || strings.ContainsFunc(name, unicode.IsControl) || len(name) > 4096 || !filepath.IsLocal(name) {
		return "", fmt.Errorf("%w: invalid archive entry path", ErrUnsafe)
	}
	for part := range strings.SplitSeq(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("%w: invalid archive path component", ErrUnsafe)
		}
	}
	return name, nil
}

type archiveEntry struct {
	name string
	size int64
	dir  bool
}

// parseListing accepts only unambiguous 7-Zip technical-list records. File data
// is subsequently streamed to Go; 7-Zip never receives an extraction directory.
func parseListing(list string, maxBytes int64) ([]archiveEntry, error) {
	list = strings.ReplaceAll(list, "\r\n", "\n")
	// BetterZip's bundled build emits tab-separated aliases instead of the
	// upstream multiline technical format. Preserve spaces in paths and values.
	compact := strings.HasPrefix(list, "path=")
	recordSeparator, fieldSeparator := "\n\n", "\n"
	if compact {
		recordSeparator, fieldSeparator = "\n", "\t"
	}
	aliases := map[string]string{
		"path": "Path", "fold": "Folder", "unco": "Size", "comp": "Packed Size",
		"time": "Modified", "attr": "Attributes", "type": "Encrypted",
		"comt": "Comment", "crc_": "CRC", "meth": "Method", "os__": "Host OS",
		"Created": "Created", "Accessed": "Accessed", "Characteristics": "Characteristics",
		"Version": "Version", "Volume Index": "Volume Index", "Offset": "Offset", "Block": "Block",
	}
	var entries []archiveEntry
	seen := make(map[string]bool)
	var total int64
	for block := range strings.SplitSeq(strings.TrimRight(list, "\n"), recordSeparator) {
		if block == "" {
			continue
		}
		fields := make(map[string]string)
		if compact {
			block = strings.TrimSuffix(block, "\t")
		}
		for line := range strings.SplitSeq(block, fieldSeparator) {
			key, value, ok := strings.Cut(line, " = ")
			if compact {
				key, value, ok = strings.Cut(line, "=")
				canonical, known := aliases[key]
				if !known {
					return nil, fmt.Errorf("%w: unknown compact archive field", ErrUnsafe)
				}
				key = canonical
			}
			if _, duplicate := fields[key]; !ok || duplicate {
				return nil, fmt.Errorf("%w: ambiguous archive metadata", ErrUnsafe)
			}
			fields[key] = value
		}
		name, err := archiveName(fields["Path"])
		if err != nil {
			return nil, err
		}
		attributes := fields["Attributes"]
		if fields["Encrypted"] == "+" || fields["Symbolic Link"] != "" || fields["Hard Link"] != "" || strings.HasPrefix(fields["Mode"], "l") || strings.HasPrefix(attributes, "l") || strings.Contains(attributes, " l") {
			return nil, fmt.Errorf("%w: encrypted entries and links are unsupported", ErrUnsafe)
		}
		dir := fields["Folder"] == "+" || strings.HasPrefix(attributes, "D") || strings.HasPrefix(attributes, "d") || strings.HasPrefix(fields["Mode"], "d")
		size, err := strconv.ParseInt(fields["Size"], 10, 64)
		if err != nil || size < 0 || (dir && size != 0) {
			return nil, fmt.Errorf("%w: invalid entry size", ErrUnsafe)
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate or case-colliding archive entries", ErrUnsafe)
		}
		if size > maxBytes-total || len(entries) == maxEntries {
			return nil, ErrLimit
		}
		total += size
		seen[key] = dir
		entries = append(entries, archiveEntry{name, size, dir})
	}
	for _, entry := range entries {
		for parent := filepath.Dir(strings.ToLower(entry.name)); parent != "."; parent = filepath.Dir(parent) {
			if dir, exists := seen[parent]; exists && !dir {
				return nil, fmt.Errorf("%w: file/directory path collision", ErrUnsafe)
			}
		}
	}
	return entries, nil
}

package betterzip

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// All fixtures live under this original project, never in a project checkout copy.
func testDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".test-")
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(abs); err != nil {
			t.Error(err)
		}
	})
	return abs
}

func testService(t *testing.T, root string) *Service {
	t.Helper()
	s, err := New(Config{Root: root, Timeout: DefaultTimeout, MaxBytes: DefaultMaxBytes})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestArchiveName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../escape", "a/../escape", "/tmp/escape", `C:\escape`, "a//b", "a\nPath = forged", "x:y"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			if _, err := archiveName(name); !errors.Is(err, ErrUnsafe) {
				t.Fatalf("accepted unsafe name %q: %v", name, err)
			}
		})
	}
	for _, name := range []string{"报告/a b.txt", "-switch", "@listfile", "file*literal", "folder/"} {
		if _, err := archiveName(name); err != nil {
			t.Fatalf("valid name %q: %v", name, err)
		}
	}
}

func TestListingRejectsUnsafeMetadata(t *testing.T) {
	record := func(name, size string) string { return "Path = " + name + "\nSize = " + size + "\nFolder = -\n" }
	for name, list := range map[string]string{
		"traversal":     record("../escape", "1"),
		"duplicate":     record("a", "1") + "\n" + record("A", "1"),
		"file-parent":   record("a", "1") + "\n" + record("a/b", "1"),
		"negative":      record("a", "-1"),
		"symlink":       record("a", "1") + "Symbolic Link = ../outside\n",
		"encrypted":     record("a", "1") + "Encrypted = +\n",
		"field-forgery": record("a\nSize = 1", "1"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseListing(list, 20); !errors.Is(err, ErrUnsafe) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, err := parseListing(record("a", "21"), 20); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit: %v", err)
	}
	if got, err := parseListing(record("a", "20"), 20); err != nil || len(got) != 1 {
		t.Fatalf("valid listing: %v %v", got, err)
	}
	if _, err := parseListing(record("a", "1")+"Comment = \n", 20); err != nil {
		t.Fatalf("empty trailing field: %v", err)
	}
	if got, err := parseListing("path=a\tunco=1\tattr=-rw-------\ttype=-\tBlock=\t\n", 20); err != nil || len(got) != 1 {
		t.Fatalf("compact listing: %v %v", got, err)
	}
	if _, err := parseListing("path=a\tunco=1\tattr=lrwxrwxrwx\ttype=-\t\n", 20); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("compact symlink: %v", err)
	}
}

func TestRootAndLinkBoundaries(t *testing.T) {
	base := testDir(t)
	rootPath := filepath.Join(base, "root")
	if err := os.Mkdir(rootPath, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	s := testService(t, rootPath)
	if _, err := relativePath(rootPath, outside); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("outside accepted: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(rootPath, "symlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := regularFile(s.root, "symlink"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlink accepted: %v", err)
	}
	if err := os.Link(outside, filepath.Join(rootPath, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := regularFile(s.root, "hardlink"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("hardlink accepted: %v", err)
	}
	if _, err := s.Compress(t.Context(), rootPath, "x.zip", []string{"../outside.txt"}); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("outside source accepted: %v", err)
	}
}

func TestMembershipDoesNotAccessFilesOrHelper(t *testing.T) {
	s := testService(t, testDir(t))
	s.config.SevenZip = "/nonexistent/helper"
	server := NewMCPServer(s)
	if got := len(server.ListTools()); got != 4 {
		t.Fatalf("got %d tools", got)
	}
	for name, extra := range map[string]string{
		"betterzip_encrypt_compress": `,"password":"do-not-echo-this"`,
		"betterzip_split_compress":   `,"split_size":"100m"`,
	} {
		tool := server.GetTool(name)
		var request mcp.CallToolRequest
		if err := json.Unmarshal([]byte(`{"params":{"name":"`+name+`","arguments":{"working_dir":"/not-there","output_archive":"a.zip","sources":["not-there"]`+extra+`}}}`), &request); err != nil {
			t.Fatal(err)
		}
		result, err := tool.Handler(t.Context(), request)
		if err != nil || result.IsError {
			t.Fatalf("membership accessed dependency: %v %v", result, err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "do-not-echo-this") {
			t.Fatal("password leaked")
		}
	}
}

func TestStrictArguments(t *testing.T) {
	for _, raw := range []string{
		`{"working_dir":".","sources":["a"],"output_archive":"a.zip","password":null}`,
		`{"working_dir":".","working_dir":"/","sources":["a"],"output_archive":"a.zip"}`,
		`{"working_dir":".","sources":"a","output_archive":"a.zip"}`,
	} {
		var args compressArgs
		request := mcp.CallToolRequest{Params: mcp.CallToolParams{RawArguments: json.RawMessage(raw)}}
		if err := decodeArguments(request, &args); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
}

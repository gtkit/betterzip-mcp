package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/gtkit/betterzip-mcp/internal"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "betterzip-mcp:", err)
		os.Exit(1)
	}
}

func run() (err error) {
	if len(os.Args) > 1 && os.Args[1] == "--internal-sevenzip" {
		return betterzip.ExecSevenZip(os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if runtime.GOOS != "darwin" {
			return errors.New("only macOS is supported")
		}
		return runSetup(os.Args[2:], os.Stdin, os.Stderr)
	}
	flags := flag.NewFlagSet("betterzip-mcp", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: betterzip-mcp [options]\n       betterzip-mcp setup [--root directory]")
		flags.PrintDefaults()
	}
	root := flags.String("root", "", "Authorized data root; overrides environment and saved user settings")
	helper := flags.String("sevenzip", os.Getenv("BETTERZIP_MCP_7ZA"), "Trusted absolute path to BetterZip's Helpers/7za; default automatic discovery")
	timeout := flags.Duration("timeout", betterzip.DefaultTimeout, "Positive total timeout per tool call")
	maxBytes := flags.Int64("max-bytes", betterzip.DefaultMaxBytes, "Maximum source/extracted bytes per call (1..1 PiB)")
	version := flags.Bool("version", false, "Print version to stderr and exit")
	diagnose := flags.Bool("diagnose", false, "Read-only helper/client discovery; print guidance to stderr")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *version {
		fmt.Fprintln(os.Stderr, betterzip.Version)
		return nil
	}
	if runtime.GOOS != "darwin" {
		return errors.New("only macOS is supported")
	}
	var override *string
	flags.Visit(func(value *flag.Flag) {
		if value.Name == "root" {
			override = root
		}
	})
	config := betterzip.Config{SevenZip: *helper, Timeout: *timeout, MaxBytes: *maxBytes}
	if *diagnose {
		var rootPath, rootSource string
		rootPath, rootSource, err = betterzip.ResolveRoot(override)
		if err != nil {
			return err
		}
		if rootPath == "" {
			return betterzip.ErrNotConfigured
		}
		config.Root = rootPath
		var s *betterzip.Service
		s, err = betterzip.New(config)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, s.Close()) }()
		fmt.Fprintln(os.Stderr, "Authorized root:", s.RootPath())
		fmt.Fprintln(os.Stderr, "Root source:", rootSource)
		path, err := betterzip.FindSevenZip(*helper)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "BetterZip helper:", path)
		for _, client := range []string{"codex", "cursor"} {
			if path, err := exec.LookPath(client); err == nil {
				fmt.Fprintln(os.Stderr, client+":", path)
			}
		}
		fmt.Fprintln(os.Stderr, "No client configuration was changed. See README.md for manual setup.")
		return nil
	}
	mcpServer, err := betterzip.NewUserMCPServer(config, override)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	transport := server.NewStdioServer(mcpServer)
	for _, option := range []server.StdioOption{server.WithErrorLogger(log.New(os.Stderr, "betterzip-mcp: ", log.LstdFlags)), server.WithWorkerPoolSize(2), server.WithQueueSize(16)} {
		option(transport)
	}
	// mcp-go cancels and joins in-flight requests when stdin reaches EOF.
	err = transport.Listen(ctx, os.Stdin, os.Stdout)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// runSetup is an explicit user action, separate from the MCP stdio transport.
// Its prompt and results stay on stderr, even when stdin is piped.
func runSetup(args []string, input io.Reader, output io.Writer) error {
	flags := flag.NewFlagSet("betterzip-mcp setup", flag.ContinueOnError)
	flags.SetOutput(output)
	root := flags.String("root", "", "Existing folder to authorize; omit to choose interactively")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("use betterzip-mcp setup --root <directory>")
	}
	if *root == "" {
		if _, err := fmt.Fprintln(output, "请选择允许压缩和解压的文件夹。可输入 ~/Downloads，或在 Finder 中用 ⌥⌘C 复制文件夹路径后粘贴。"); err != nil {
			return err
		}
		if _, err := fmt.Fprint(output, "授权文件夹（留空取消）："); err != nil {
			return err
		}
		line, err := bufio.NewReader(io.LimitReader(input, 4098)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		*root = strings.TrimSpace(line)
		if *root == "" {
			return errors.New("未选择文件夹，设置已取消")
		}
	}
	filename, err := betterzip.SaveRoot(*root)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "授权目录已保存，后续操作将使用此设置；以后无需每次设置。旧版连接需重新连接后生效。\n用户配置文件：", filename)
	return err
}

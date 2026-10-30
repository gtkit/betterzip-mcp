package betterzip

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

var errSelectionCanceled = errors.New("已取消文件夹选择，授权设置未更改")

const folderSelectionScript = `try
    return POSIX path of (choose folder with prompt "选择允许 BetterZip MCP 压缩和解压的文件夹。授权仅包括所选文件夹及其子文件夹；可随时要求修改授权目录。")
on error number -128
    return ""
end try`

// authorization keeps only the in-flight selection shared. Each archive call
// owns its root handle, so changing settings cannot close another call's root.
type authorization struct {
	config   Config
	override *string
	picker   func(context.Context) (string, error)
	mu       sync.Mutex
	pending  *folderSelection
}

type folderSelection struct {
	done chan struct{}
	root string
	err  error
}

// NewUserMCPServer adds native folder selection to the ordinary stdio server.
// It performs no interactive work until a file operation or configuration call.
func NewUserMCPServer(config Config, override *string) (*server.MCPServer, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if override != nil {
		override = new(*override)
	}
	a := &authorization{config: config, override: override, picker: chooseFolder}
	// Explicit startup overrides retain their validation and precedence.
	if override != nil || os.Getenv("BETTERZIP_MCP_ROOT") != "" {
		root, _, err := ResolveRoot(override)
		if err != nil {
			return nil, err
		}
		config.Root = root
		service, err := New(config)
		if err != nil {
			return nil, err
		}
		if err := service.Close(); err != nil {
			return nil, err
		}
	}
	return a.server(), nil
}

func (a *authorization) server() *server.MCPServer {
	return newMCPServer(func(ctx context.Context) (*Service, bool, error) {
		s, err := a.open(ctx)
		return s, true, err
	}, func(ctx context.Context) (string, error) { return a.selectRoot(ctx, true) })
}

func (a *authorization) open(ctx context.Context) (*Service, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, _, err := ResolveRoot(a.override)
	if err != nil {
		return nil, err
	}
	if root == "" {
		root, err = a.selectRoot(ctx, false)
		if err != nil {
			return nil, err
		}
	}
	config := a.config
	config.Root = root
	service, err := New(config)
	if err != nil {
		return nil, fmt.Errorf("授权目录不可用，请让 BetterZip 修改授权目录：%w", err)
	}
	return service, nil
}

func (a *authorization) selectRoot(ctx context.Context, force bool) (root string, err error) {
	if a.override != nil || os.Getenv("BETTERZIP_MCP_ROOT") != "" {
		return "", errors.New("授权目录由 --root 或 BETTERZIP_MCP_ROOT 指定；请先移除该覆盖设置，再通过文件夹选择窗口修改")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	a.mu.Lock()
	if pending := a.pending; pending != nil {
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-pending.done:
			return pending.root, pending.err
		}
	}
	pending := &folderSelection{done: make(chan struct{})}
	a.pending = pending
	a.mu.Unlock()

	defer func() {
		pending.root, pending.err = root, err
		if pending.root == "" && pending.err == nil {
			pending.err = errors.New("授权目录选择未完成")
		}
		a.mu.Lock()
		a.pending = nil
		a.mu.Unlock()
		close(pending.done)
	}()
	return a.pickAndSave(ctx, force)
}

func (a *authorization) pickAndSave(ctx context.Context, force bool) (string, error) {
	if !force {
		root, _, err := ResolveRoot(a.override)
		if err != nil || root != "" {
			return root, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	root, err := a.picker(ctx)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if root == "" {
		return "", errSelectionCanceled
	}
	if _, err := SaveRoot(root); err != nil {
		return "", err
	}
	return root, nil
}

func chooseFolder(ctx context.Context) (string, error) {
	command := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", folderSelectionScript)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 2 * time.Second
	var stdout, stderr diagnosticBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("无法打开文件夹选择窗口，请确认当前有可用的 macOS 桌面会话：%w", err)
	}
	root := strings.TrimSuffix(strings.TrimSuffix(stdout.String(), "\n"), "\r")
	if root == "" {
		return "", errSelectionCanceled
	}
	if len(root) > 4096 {
		return "", ErrLimit
	}
	return root, nil
}

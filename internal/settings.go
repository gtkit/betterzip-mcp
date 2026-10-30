package betterzip

import (
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotConfigured keeps an unconfigured server discoverable without granting
// access to its installation directory or any other implicit filesystem root.
var ErrNotConfigured = errors.New("尚未设置授权目录。请让 BetterZip 设置授权目录，在弹出的窗口选择允许读写的文件夹；终端也可运行 betterzip-mcp setup")

const maxSettingsBytes = 8192

type settings struct {
	Root string `json:"root"`
}

// SettingsPath belongs to the current user, never to the plugin installation.
func SettingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user configuration: %w", err)
	}
	return filepath.Join(dir, "betterzip-mcp", "config.json"), nil
}

// ResolveRoot applies command line, environment, then saved user settings.
// A nil override means --root was omitted. Missing settings grant no access.
func ResolveRoot(override *string) (root, source string, err error) {
	if override != nil {
		if strings.TrimSpace(*override) == "" {
			return "", "", fmt.Errorf("%w: --root cannot be empty", ErrInvalid)
		}
		return *override, "--root", nil
	}
	if value := os.Getenv("BETTERZIP_MCP_ROOT"); value != "" {
		return value, "BETTERZIP_MCP_ROOT", nil
	}
	filename, err := SettingsPath()
	if err != nil {
		return "", "", err
	}
	dir, err := os.OpenRoot(filepath.Dir(filename))
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("open user settings: %w", err)
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	file, err := regularFile(dir, filepath.Base(filename))
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("open user settings: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	data, err := io.ReadAll(io.LimitReader(file, maxSettingsBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("read user settings: %w", err)
	}
	var saved settings
	decodeErr := json.Unmarshal(data, &saved, json.RejectUnknownMembers(true))
	anchored := filepath.IsAbs(saved.Root) || saved.Root == "~" || strings.HasPrefix(saved.Root, "~/")
	if len(data) > maxSettingsBytes || decodeErr != nil || strings.TrimSpace(saved.Root) == "" || !anchored {
		return "", "", errors.New("授权目录设置无效，请让 BetterZip 修改授权目录，或重新运行 betterzip-mcp setup；未使用默认目录")
	}
	return saved.Root, "用户设置", nil
}

// SaveRoot validates before changing settings. The atomic rename is the commit
// point; write/validation failures leave the previous configuration intact.
func SaveRoot(value string) (filename string, err error) {
	service, err := New(Config{Root: value, Timeout: DefaultTimeout, MaxBytes: DefaultMaxBytes})
	if err != nil {
		return "", fmt.Errorf("授权目录不可用，设置未保存: %w", err)
	}
	root := service.RootPath()
	if err := service.Close(); err != nil {
		return "", err
	}
	// Keep home-relative selections portable and avoid embedding a username.
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		if home, homeErr = filepath.EvalSymlinks(home); homeErr == nil {
			if relative, relErr := filepath.Rel(home, root); relErr == nil {
				if relative == "." {
					root = "~"
				} else if filepath.IsLocal(relative) {
					root = filepath.Join("~", relative)
				}
			}
		}
	}
	data, err := json.Marshal(settings{Root: root})
	if err != nil {
		return "", err
	}
	filename, err = SettingsPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return "", fmt.Errorf("create user settings directory: %w", err)
	}
	dir, err := os.OpenRoot(filepath.Dir(filename))
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	name := ".config-" + rand.Text()
	file, err := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer func() {
		if removeErr := dir.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	_, writeErr := file.Write(append(data, '\n'))
	var syncErr error
	if writeErr == nil {
		syncErr = file.Sync()
	}
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		return "", fmt.Errorf("save user settings: %w", err)
	}
	if err := dir.Rename(name, filepath.Base(filename)); err != nil {
		return "", fmt.Errorf("replace user settings: %w", err)
	}
	return filename, nil
}

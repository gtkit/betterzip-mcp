package betterzip

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const compressProperties = `"working_dir":{"type":"string","minLength":1,"description":"Existing directory inside the configured root."},"output_archive":{"type":"string","minLength":1,"description":"New .zip or .7z filename, without a directory."},"sources":{"type":"array","minItems":1,"maxItems":10000,"items":{"type":"string","minLength":1},"description":"Paths inside working_dir; relative paths resolve there."}`

var splitSizePattern = regexp.MustCompile(`^[1-9][0-9]{0,8}[kKmMgG]$`)

type compressArgs struct {
	WorkingDir    string   `json:"working_dir"`
	OutputArchive string   `json:"output_archive"`
	Sources       []string `json:"sources"`
}

type encryptArgs struct {
	compressArgs
	Password string `json:"password"`
}

type splitArgs struct {
	compressArgs
	SplitSize string `json:"split_size"`
}

type extractArgs struct {
	ArchivePath string `json:"archive_path"`
	OutputDir   string `json:"output_dir"`
}

func decodeArguments(request mcp.CallToolRequest, target any) error {
	data := []byte(request.Params.RawArguments)
	if len(data) == 0 {
		var err error
		data, err = json.Marshal(request.Params.Arguments)
		if err != nil {
			return fmt.Errorf("%w: malformed arguments", ErrInvalid)
		}
	}
	if err := json.Unmarshal(data, target, json.RejectUnknownMembers(true)); err != nil {
		// Do not echo raw input: it may contain a password or private filenames.
		return fmt.Errorf("%w: arguments must match the tool's JSON schema", ErrInvalid)
	}
	return nil
}

func (a compressArgs) validate() error {
	if strings.TrimSpace(a.WorkingDir) == "" || strings.TrimSpace(a.OutputArchive) == "" || len(a.Sources) == 0 || len(a.Sources) > maxEntries {
		return fmt.Errorf("%w: working_dir, output_archive and 1..10000 sources are required", ErrInvalid)
	}
	for _, source := range a.Sources {
		if strings.TrimSpace(source) == "" {
			return fmt.Errorf("%w: sources cannot contain empty paths", ErrInvalid)
		}
	}
	return nil
}

func toolError(err error) *mcp.CallToolResult {
	var message string
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		message = "操作超时，已终止 7za 子进程。请缩小输入规模或增大 --timeout。"
	case errors.Is(err, context.Canceled):
		message = "操作已取消，已终止 7za 子进程。"
	default:
		message = err.Error()
	}
	return mcp.NewToolResultError(message)
}

// NewMCPServer registers only local tools. It exposes no sampling, LLM, key,
// payment, configuration-writing or remote-network capability.
func NewMCPServer(service *Service) *server.MCPServer {
	s := server.NewMCPServer("betterzip-mcp", Version, server.WithToolCapabilities(false), server.WithRecovery())
	schema := func(properties, required string) []byte {
		return []byte(`{"type":"object","additionalProperties":false,"properties":{` + properties + `},"required":[` + required + `]}`)
	}
	compressionSchema := schema(compressProperties, `"working_dir","output_archive","sources"`)
	s.AddTool(mcp.NewTool("betterzip_compress",
		mcp.WithDescription("Free: create a new ZIP or 7z using BetterZip's bundled 7za. All sources must stay within working_dir and the configured root. Never overwrites."),
		mcp.WithRawInputSchema(compressionSchema)),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args compressArgs
			if err := decodeArguments(request, &args); err != nil {
				return toolError(err), nil
			}
			if err := args.validate(); err != nil {
				return toolError(err), nil
			}
			if service == nil {
				return toolError(ErrNotConfigured), nil
			}
			path, err := service.Compress(ctx, args.WorkingDir, args.OutputArchive, args.Sources)
			if err != nil {
				return toolError(err), nil
			}
			return mcp.NewToolResultText("压缩完成：" + path), nil
		})
	s.AddTool(mcp.NewTool("betterzip_extract",
		mcp.WithDescription("Free: extract an unencrypted ZIP or 7z to a new directory inside the configured root. Rejects unsafe entries and existing output directories."),
		mcp.WithRawInputSchema(schema(`"archive_path":{"type":"string","minLength":1},"output_dir":{"type":"string","minLength":1}`, `"archive_path","output_dir"`))),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args extractArgs
			if err := decodeArguments(request, &args); err != nil {
				return toolError(err), nil
			}
			if strings.TrimSpace(args.ArchivePath) == "" || strings.TrimSpace(args.OutputDir) == "" {
				return toolError(ErrInvalid), nil
			}
			if service == nil {
				return toolError(ErrNotConfigured), nil
			}
			path, err := service.Extract(ctx, args.ArchivePath, args.OutputDir)
			if err != nil {
				return toolError(err), nil
			}
			return mcp.NewToolResultText("解压完成：" + path), nil
		})
	s.AddTool(mcp.NewTool("betterzip_encrypt_compress",
		mcp.WithDescription("Membership information only. Does not create archives, run 7za or store passwords."),
		mcp.WithRawInputSchema(schema(compressProperties+`,"password":{"type":"string","minLength":1,"maxLength":1024}`, `"working_dir","output_archive","sources","password"`))),
		func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args encryptArgs
			if err := decodeArguments(request, &args); err != nil {
				return toolError(err), nil
			}
			if err := args.validate(); err != nil {
				return toolError(err), nil
			}
			if args.Password == "" || len(args.Password) > 1024 {
				return toolError(ErrInvalid), nil
			}
			return mcp.NewToolResultText("加密压缩是本插件预留的会员功能，本次未执行压缩，密码未保存。请使用已购买的 BetterZip 在本机完成加密；本插件尚未接入会员购买或验证服务。"), nil
		})
	s.AddTool(mcp.NewTool("betterzip_split_compress",
		mcp.WithDescription("Membership information only. Does not create split archives or run 7za."),
		mcp.WithRawInputSchema(schema(compressProperties+`,"split_size":{"type":"string","pattern":"^[1-9][0-9]{0,8}[kKmMgG]$"}`, `"working_dir","output_archive","sources","split_size"`))),
		func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args splitArgs
			if err := decodeArguments(request, &args); err != nil {
				return toolError(err), nil
			}
			if err := args.validate(); err != nil {
				return toolError(err), nil
			}
			if !splitSizePattern.MatchString(args.SplitSize) {
				return toolError(ErrInvalid), nil
			}
			return mcp.NewToolResultText("分卷压缩是本插件预留的会员功能，本次未执行压缩。请使用已购买的 BetterZip 在本机创建分卷；本插件尚未接入会员购买或验证服务。"), nil
		})
	return s
}

# 更新记录

## [0.1.0] - 2026-10-30

### 功能

- 提供本地 stdio MCP 服务，让 AI 客户端调用 BetterZip 随附的 7za。
- 免费创建 ZIP、7z 压缩包，并将未加密的 ZIP、7z 解压到新目录。
- 提供 `setup` 交互设置和 `setup --root`，持久保存用户选择的授权目录；未配置时不默认授权任何目录。
- 提供 Codex 插件市场清单、启动器和 macOS Apple Silicon / Intel 二进制；下载器校验 SHA-256 和目标架构。
- 限制文件访问范围、归档条目和总字节数；支持取消、超时以及失败清理，已有输出不覆盖。

### 已知范围

- 仅支持 macOS，需要用户已安装 BetterZip。
- 加密压缩和分卷压缩仅返回会员功能说明，不生成文件；不支持解压加密包。
- Apple Silicon 产物已交叉编译，尚未在 Apple Silicon 实机运行；Claude Desktop / Cursor 的界面内工具发现尚未实测。
- npm 发布暂缓，当前公开安装说明以 GitHub Release 和 Codex 插件市场为主。

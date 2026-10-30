# BetterZip MCP Server

让 Codex、Claude 等 AI 工具调用 Mac 上的 BetterZip。安装并连接后，直接说“把这个文件夹压缩成 ZIP”即可，无需编写代码。支持 ZIP、7z 的压缩与解压。

> **发布状态：[GitHub v0.1.2 Release](https://github.com/gtkit/betterzip-mcp/releases/tag/v0.1.2) 已发布，包含首次使用文件夹选择、持久保存及聊天中修改授权目录。通过下方两条 Codex 命令安装后，运行程序由启动器自动下载并校验。npm 发布继续暂缓。**

## 安装到你使用的 AI 工具

选择自己的客户端，按对应步骤操作：

- [Codex：添加插件市场与安装插件](#codex-原生插件安装)
- [Codex：MCP 接入](#codex-mcp-接入)
- [Claude Code](#claude-code)
- [Claude Desktop 或 Cursor](#claude-desktop-或-cursor)
- [安装后怎么使用](#安装后怎么使用)
- [如何选择允许访问的文件夹](#如何选择允许访问的文件夹)

所有方式都需要：

- 一台 Mac，以及已经安装的 BetterZip。
- 已安装你准备使用的 AI 客户端。
- Codex 原生插件需要 Node.js 20 或更高版本；npm 安装路线也需要 Node.js。直接运行 GitHub Release 二进制不需要 Node.js 或 Go。

下面需要输入命令的地方，都在 Mac 的“终端”应用中操作：按 `⌘ + 空格`，搜索“终端”，打开后粘贴整段命令并按回车。若提示找不到 `npm`，请先从 [Node.js 官网](https://nodejs.org/)安装 Node.js LTS，再重新打开终端。

### Codex

原生插件与直接 MCP 接入二选一，避免同名服务重复注册。两种方式共用用户保存的授权文件夹。

#### Codex 原生插件安装

使用支持 `codex plugin` 命令的 Codex，从 GitHub 安装；无需下载源码、进入项目目录或自行编译。此路线不依赖 npm 发布。**v0.1.2 已包含下述文件夹自动引导。**

1. 添加插件市场：

```bash
codex plugin marketplace add gtkit/betterzip-mcp
```

2. 安装插件：

```bash
codex plugin add betterzip-mcp@gtkit-local-tools
```

3. 重新连接插件或重新打开 Codex。首次启动会自动下载匹配架构的运行程序并校验，无需再手动下载二进制。下载进度/错误显示在 MCP 日志中；命令行可用 `/mcp` 查看连接状态。

4. 在聊天中说“设置 BetterZip 授权目录”，或直接发起压缩/解压。尚未授权时，会打开 macOS 文件夹选择窗口；选择允许处理的文件夹即可。保存后，后续操作和新会话沿用该选择。取消选择不会授权任何目录，也不会执行本次压缩/解压。

后续需要更换时，在聊天中说“修改 BetterZip 授权目录”。新操作会使用重新选择的目录，无需卸载重装或手动修改配置；已开始的操作使用开始时的授权范围完成。

选择原生插件时无需执行下面的 `codex mcp add`。以上操作由用户手动执行，MCP 服务不会自行安装插件或更改客户端配置。详见 [Codex 官方插件与市场说明](https://developers.openai.com/plugins/build/plugins)。

#### Codex MCP 接入

**直接使用 GitHub Release 二进制：** 此方式适合不使用插件启动器的用户，需要自行下载。v0.1.2 程序：[Apple Silicon](https://github.com/gtkit/betterzip-mcp/releases/download/v0.1.2/betterzip-mcp-darwin-arm64)、[Intel](https://github.com/gtkit/betterzip-mcp/releases/download/v0.1.2/betterzip-mcp-darwin-amd64)。用 `shasum -a 256 文件名` 与 Release 的 [SHA256SUMS](https://github.com/gtkit/betterzip-mcp/releases/download/v0.1.2/SHA256SUMS) 对照。给下载文件添加执行权限并设置目录，例如 Apple Silicon：

```bash
chmod +x ./betterzip-mcp-darwin-arm64
./betterzip-mcp-darwin-arm64 setup
```

Intel 用户将文件名替换为 `betterzip-mcp-darwin-amd64`。在 Finder 中选中该程序，按 `⌥ + ⌘ + C` 复制完整路径，替换下面引号中的文字：

```bash
codex mcp add betterzip -- "粘贴下载程序的完整路径"
```

重新启动 Codex，用 `/mcp` 确认 `betterzip` 已连接。此方式直接运行二进制，不需要 Node.js，也不需要 npm；请把下载程序保存在会长期保留的位置。

**通过 npm 安装（尚未发布）：**

1. **以下 npm 路线在 npm 正式发布后可用。** 届时在终端复制并执行以下命令：

```bash
npm install -g @gtkit/betterzip-mcp
betterzip-mcp setup
codex mcp add betterzip -- \
  "$(npm root -g)/@gtkit/betterzip-mcp/bin/betterzip-mcp"
```

2. 完全退出 Codex，再重新打开。
3. 确认 MCP 工具列表中已连接 `betterzip`，然后按[使用示例](#安装后怎么使用)直接提问。使用 Codex 命令行时，也可以输入 `/mcp` 查看连接状态。

运行 `setup` 时按提示输入或粘贴你允许处理的文件夹路径。选择会保存在你自己的设置中，重启后继续生效；安装命令会自动找到程序，不需要填写开发者的项目路径。

### Claude Code

#### 原生插件市场（需适配）

Claude Code 也支持添加插件市场后安装插件。以下仅为命令格式示例，`owner/repo` 和 `plugin-name@marketplace-name` 需要替换为已适配的仓库与插件标识：

```bash
claude plugin marketplace add owner/repo
claude plugin install plugin-name@marketplace-name
```

本项目尚未提供 Claude Code 所需的 `.claude-plugin/marketplace.json`，也未完成其插件与 MCP 启动配置适配；现有 Codex 市场不能直接作为 Claude Code 市场使用。当前请使用下面的 MCP 接入方式。详见 [Claude Code 官方插件安装说明](https://code.claude.com/docs/en/discover-plugins)。

#### 直接 MCP 接入

1. **以下 npm 路线在 npm 正式发布后可用。** 届时在终端复制并执行：

```bash
npm install -g @gtkit/betterzip-mcp
betterzip-mcp setup
claude mcp add --transport stdio --scope user betterzip -- \
  "$(npm root -g)/@gtkit/betterzip-mcp/bin/betterzip-mcp"
```

2. 重新启动 Claude Code，输入 `/mcp`。
3. 确认 `betterzip` 已连接，然后直接提出压缩或解压需求。

这里的 `--scope user` 表示一次配置供当前用户的各个项目使用。`setup` 保存的文件夹由当前用户自己选择。详见 [Claude Code 官方 MCP 文档](https://code.claude.com/docs/en/mcp)。

### Claude Desktop 或 Cursor

#### Claude Desktop 扩展包（需适配）

Claude Desktop 支持将本地 MCP 打包为 `.mcpb` 扩展。在设置的 Desktop app → Extensions → Manage 中打开 Advanced settings，选择 Install Extension，再选择扩展包；旧版入口可能显示为 Settings → Extensions。

本项目尚未提供 `.mcpb` 扩展包，需要补充对应清单和打包适配后才能使用这种安装方式。当前请使用下面的手动 MCP 配置。详见 [Claude Desktop 官方扩展安装说明](https://support.claude.com/en/articles/10949351-getting-started-with-local-mcp-servers-on-claude-desktop)。

#### Cursor 原生插件与 MCP 安装链接（尚未提供）

Cursor 支持在 Customize 中从插件市场安装插件。原生插件需要符合其支持的格式，例如根目录的 `plugin.json` 或 `.cursor-plugin/plugin.json`；本项目目前只有 Codex 插件清单，尚未完成 Cursor 原生插件适配。

Cursor 也支持通过 `cursor://anysphere.cursor-deeplink/mcp/install?name=名称&config=Base64配置` 分享 MCP 安装链接。本项目尚未提供可直接使用的链接；生成链接时仍需确定本项目程序的安装方式和各用户机器上的启动配置。当前请使用下面的手动 MCP 配置。详见 [Cursor 官方插件与 MCP 安装链接说明](https://cursor.com/docs/plugins)。

#### 手动 MCP 接入

1. **以下 npm 路线在 npm 正式发布后可用。** 届时在终端执行安装命令：

```bash
npm install -g @gtkit/betterzip-mcp
betterzip-mcp setup
```

按 `setup` 的提示选择授权文件夹，完成后继续。

2. 再执行下面这一行，**复制它显示的完整程序路径**：

```bash
printf '%s\n' "$(npm root -g)/@gtkit/betterzip-mcp/bin/betterzip-mcp"
```

3. 打开客户端的本地 MCP 配置：

   - **Claude Desktop**：在设置中找到本地 MCP 服务器配置，打开配置文件。不同版本入口位置可能不同，可参照 [Claude Desktop 官方说明](https://support.claude.com/en/articles/10949351-getting-started-with-local-mcp-servers-on-claude-desktop)。
   - **Cursor**：在设置中打开 MCP 配置；全局配置文件为 `~/.cursor/mcp.json`。参见 [Cursor 官方说明](https://cursor.com/docs/mcp)。

4. 添加以下配置，把 `command` 引号里的文字替换为刚才复制的程序路径。如果文件里已有其他服务，只添加 `betterzip` 这一项，保留原来的内容：

```json
{
  "mcpServers": {
    "betterzip": {
      "type": "stdio",
      "command": "粘贴上一步显示的完整程序路径"
    }
  }
}
```

5. 保存配置，重新加载 MCP 或完全退出并重新打开客户端，确认 `betterzip` 已连接。

授权文件夹从 `setup` 保存的用户设置读取；无需在每个客户端重复填写。上述配置已提供，Claude Desktop / Cursor 的界面内工具发现尚未实测。

### 接入方式与支持状态

| 客户端 | 本项目已提供 | 仍需适配或交付 |
| --- | --- | --- |
| Codex | GitHub 原生插件安装、Release 二进制与授权设置；直接 MCP 接入 | npm 发布暂缓 |
| Claude Code | 直接 MCP 接入 | 原生插件市场配置及启动适配 |
| Claude Desktop | 手动 MCP 配置 | `.mcpb` 一键安装扩展包 |
| Cursor | 手动 MCP 配置 | 原生插件适配、可直接使用的 MCP 安装链接 |

“已提供”指项目已有对应入口或配置，npm 安装仍需等待 npm 公开发布；Claude Desktop / Cursor 的界面内工具发现尚未实测。客户端支持插件安装，不代表本项目已经提供该客户端的插件包。

## 安装后怎么使用

假设你授权的是“下载”文件夹，可以在 AI 客户端里直接说：

- “把下载文件夹里的‘资料’文件夹压缩成资料.zip。”
- “将下载文件夹里的图片文件夹打包成图片.7z。”
- “把下载文件夹里的资料.zip 解压到资料_解压文件夹。”
- “先告诉我将要处理哪些文件，再开始压缩。”

源文件需要在授权文件夹内；输出压缩包或解压文件夹必须使用新名称，已有文件不会被覆盖。

| 功能 | 当前行为 |
| --- | --- |
| ZIP / 7z 压缩 | 免费，实际执行 |
| ZIP / 7z 解压 | 免费，实际执行；不支持解压加密包 |
| 加密压缩 | 仅返回会员功能说明，不执行压缩 |
| 分卷压缩 | 仅返回会员功能说明，不生成分卷 |

## 如何选择允许访问的文件夹

“授权文件夹”是你允许工具读写的位置。每位用户自己选择，不会默认使用开发者目录、插件安装目录或整个主目录。

v0.1.2 提供原生文件夹选择：在聊天中说“设置 BetterZip 授权目录”；首次压缩或解压未配置时也会弹出选择窗口。界面显示的范围是所选文件夹及其子文件夹，模型不能传入路径代替你的选择。取消、超时或保存失败不改变已有选择。窗口最多等待两分钟，可再次发起。

想修改时说“修改 BetterZip 授权目录”，再次选择并保存即可。新请求立即使用新设置，已经开始的操作仍按原授权快照完成。旧版或偏好终端的用户仍可运行：

通过 GitHub 下载程序的用户，请把下文的 `betterzip-mcp` 换成下载程序的实际路径，例如 `./betterzip-mcp-darwin-arm64`。npm 发布并安装后，才可直接使用 `betterzip-mcp` 命令。

```bash
betterzip-mcp setup
```

按提示输入文件夹路径。可以输入 `~/Downloads` 使用自己的“下载”文件夹，也可以在 Finder 中选中文件夹，按 `⌥ + ⌘ + C` 复制路径后粘贴。留空会取消，不改变已有设置。

知道路径时也可以直接设置，例如：

```bash
betterzip-mcp setup --root "$HOME/Downloads"
```

设置会保存到当前用户的配置文件 `~/Library/Application Support/betterzip-mcp/config.json`，独立于插件和项目目录。v0.1.2 会在每次操作开始时读取设置，因此聊天选择和 `setup` 修改都对下一次请求生效；v0.1.1 修改后需要重连。插件升级不会因为替换安装目录而丢失这份设置。

目录必须已经存在。一次设置一个授权根目录，包含其子文件夹；目录外的文件会被拒绝。未配置时也能连接并发现工具，连接本身不弹窗；只有文件操作或明确的设置请求才打开选择框。无 macOS 图形会话时选择会明确失败，可使用终端 `setup`。不会静默授权插件安装目录。

需要确认当前生效的目录和来源时运行：

```bash
betterzip-mcp --diagnose
```

优先级是：客户端显式传入的 `--root` → `BETTERZIP_MCP_ROOT` 环境变量 → 用户保存的设置。存在显式覆盖时，聊天设置入口会说明原因，不会写入一份无效设置；需由用户移除覆盖项后才能通过窗口修改。MCP 服务不会自行修改客户端配置，也不能通过模型提供的工具参数扩大授权。

## 安装或使用遇到问题

| 提示或现象 | 处理方法 |
| --- | --- |
| 找不到 `npm` | 安装 Node.js LTS，重新打开终端后再试 |
| 找不到 `codex` 或 `claude` | 确认对应客户端的命令行工具也已安装，重新打开终端后再试 |
| npm 找不到包 | npm 发布暂缓；当前使用前面的 GitHub 插件与 Release 二进制路线 |
| 下载二进制失败 | 确认指定 Release 已有对应架构附件，并且启动器所在进程能访问 GitHub |
| 浏览器能下载，但 Node 启动器连接 GitHub 超时 | 浏览器或系统代理不一定被 Node 继承；配置 Node 使用所需代理，或改用上面的 Release 二进制直接接入 MCP |
| “没有加载 MCP 工具” | 查看客户端的 MCP 连接状态，确认已连接后重启会话；“已安装”不等于“已连接” |
| “尚未设置授权目录” | v0.1.2 可在聊天中要求“设置 BetterZip 授权目录”；旧版运行 `betterzip-mcp setup` 后重连 |
| “路径超出授权目录” | 将文件放入已授权文件夹，或在聊天中要求“修改 BetterZip 授权目录”；旧版仍通过 `setup` 修改 |
| 选择窗口被取消、超时或无法打开 | 未授权或修改任何目录，可重新发起；原生选择需要可用的 macOS 桌面会话 |
| 设置后仍使用旧目录 | 用 `--diagnose` 查看来源，检查客户端是否还有 `--root` 或环境变量覆盖 |
| 找不到 BetterZip 压缩程序 | 确认 BetterZip 已安装；自定义安装位置的设置见后面的配置说明 |
| 输出文件或目录已经存在 | 换一个新名称，或先自行移动已有结果 |

本项目不要求填写模型 API Key，也不会自动修改其他客户端的配置。AI 账号、模型选择及其费用由所使用的客户端管理。

## 开发与维护

以下内容面向维护者。普通用户正式发布后按上面的客户端安装步骤操作，不需要编译源码或阅读测试命令。

### 本地试用

维护者从源码构建或本地调试时，可以在**原项目根目录**打开终端，构建后接入客户端：

```bash
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/betterzip-mcp ./cmd
node ./launch.js setup
```

需要 Go 1.27+；如果原项目已有本机可执行的 `bin/betterzip-mcp`，可跳过构建。

接入 Codex 时，在原项目根目录选择以下一种方式，避免同名服务重复注册。

**本地原生插件：**

```bash
codex plugin marketplace add "$PWD"
codex plugin add betterzip-mcp@gtkit-local-tools
```

本项目通过 `.codex-plugin/plugin.json` 和 `.agents/plugins/marketplace.json` 提供本地插件。原项目更新后，重新执行 `codex plugin add betterzip-mcp@gtkit-local-tools` 刷新安装，再重新加载 MCP 或重启客户端。

**直接注册 MCP：**

```bash
codex mcp add betterzip -- "$PWD/bin/betterzip-mcp"
```

其他客户端也可直接使用该二进制，授权目录默认读取用户保存的设置，也可用 `--root` 单独覆盖。v0.1.2 下一次请求会读取修改后的设置；旧版 v0.1.1 仍需重连。

## 工具与参数

| 工具 | 参数 | 行为 |
| --- | --- | --- |
| `betterzip_compress` | working_dir、output_archive、sources[] | 免费创建新的 `.zip` / `.7z` |
| `betterzip_extract` | archive_path、output_dir | 免费解压未加密的 ZIP/7z 到新目录 |
| `betterzip_encrypt_compress` | working_dir、output_archive、sources[]、password | 仅会员提示；不压缩、不保存/回显密码 |
| `betterzip_split_compress` | working_dir、output_archive、sources[]、split_size | 仅会员提示；不创建分卷；split_size 例如 `100m` |
| `betterzip_configure` | 无参数 | v0.1.2 新增：打开本机目录选择窗口并保存用户选择，供首次授权或之后修改；不接受模型提供的路径 |

会员接口是产品预留的纯文本提示，尚未接入购买或验证服务，不会假称已检查 BetterZip 授权，也不包含虚构的付费链接。基础功能的返回不包含营销文案。

以下示例中的路径相对于用户自己设置的授权文件夹：

```json
{
  "working_dir": "report",
  "output_archive": "report.zip",
  "sources": ["notes.txt", "images"]
}
```

```json
{
  "archive_path": "report/report.zip",
  "output_dir": "report-restored"
}
```

## 运行与分发说明

本项目使用 **BetterZip 随附的 `Contents/Helpers/7za`**。BetterZip 6 官方 CLI 名为 `betterzip`，压缩/解压会进入 GUI 异步队列，因此本服务直接管理 7za 子进程以支持取消和超时。基础压缩、解压免费；加密、分卷当前只返回会员功能提示。

npm 包只含分发脚本、清单、图标及文档，不含 Go 源码或预编译二进制。postinstall 根据 Node 的 `arm64` 或 `x64` 选择 Release 的 arm64/amd64 文件，校验 SHA-256 和 Mach-O 架构后保存到包内 `bin/betterzip-mcp`。在 Rosetta 下运行的 x64 Node 选择 amd64 文件。禁用安装脚本时，通过 `betterzip-mcp` 启动器首次运行仍会尝试下载。

公开用户通过 GitHub 安装的步骤见前面的 [Codex 原生插件安装](#codex-原生插件安装)；开发者使用本地市场的步骤见[本地试用](#本地试用)。

当前实测的 Codex CLI 为 `0.166.0-alpha.5.1`：`plugin add` 接受插件 selector，路径/GitHub 地址交给 `plugin marketplace add`；npm URL 不是合法的 plugin selector。程序不会自动修改任何客户端配置。

## 配置

| 项目 | 用途 | 默认 / 留空行为 | 约束 |
| --- | --- | --- | --- |
| `setup` / `setup --root` | 保存当前用户允许读写的目录 | 无参数时提示输入；留空取消 | 必须是已有目录，保存在用户设置中 |
| `betterzip_configure` MCP 工具 | 在本机窗口选择或修改授权目录 | 每次显式调用都会提示；首次文件操作未配置时也会提示 | 空对象参数；由用户选择；两分钟超时，取消不改配置 |
| `--root` / `BETTERZIP_MCP_ROOT` | 临时覆盖允许工具读写的数据根目录 | CLI 优先，其次环境变量，然后用户设置；都没有则等待首次用户选择 | 必须已存在，不回退到启动工作目录；存在覆盖时不能通过窗口修改 |
| `--sevenzip` / `BETTERZIP_MCP_7ZA` | 指定可信的 BetterZip `7za` | CLI 优先；留空依次检查 `/Applications/BetterZip.app/Contents/Helpers/7za`、`~/Applications/BetterZip.app/Contents/Helpers/7za` | 显式值必须是可执行文件的绝对路径 |
| `--timeout` | 单次免费工具调用的总时间上限，涵盖复制、列表与压缩/解压 | `5m` | 正 Go duration，如 `30s`、`10m`；客户端更短 deadline 优先 |
| `--max-bytes` | 单次源文件总字节 / 解压总字节上限；输入归档也受同一值限制 | `4294967296`（4 GiB） | 正整数，最多 1 PiB；按实际传输字节校验 |
| `--diagnose` | 检查根目录、7za、PATH 中的客户端并显示手动接入提示 | 不启用 | 只读，不修改任何客户端配置 |
| `--version` | 输出版本后退出 | 不启用 | 输出到 stderr |

`~` 与相对路径受支持，不展开 `$HOME` 等环境变量表达式。压缩的相对 sources 相对于 working_dir；解压的相对 archive_path/output_dir 相对于 `--root`。配置示例：

```bash
export BETTERZIP_MCP_ROOT="$HOME/Downloads"
export BETTERZIP_MCP_7ZA="/Applications/BetterZip.app/Contents/Helpers/7za"
./bin/betterzip-mcp --diagnose
```

`settings.example.json` 展示用户设置格式，仅含 `root`；项目中的示例不会被自动加载。`setup` 将实际选择写入用户配置目录。`.env.example` 仅供参考，程序不自动读取 `.env`。不要把整块磁盘作为 root 来绕过权限范围。

## Codex 插件启动配置

插件采用 Codex 兼容清单，显式将工作目录设为插件根，再从相对路径运行启动器：

```json
{
  "mcpServers": {
    "betterzip": {
      "type": "stdio",
      "command": "node",
      "args": ["./launch.js"],
      "cwd": "./",
      "env_vars": ["BETTERZIP_MCP_ROOT", "BETTERZIP_MCP_7ZA"]
    }
  }
}
```

`cwd` 的相对路径由 Codex 从插件根解析，`args` 则由 Node 在该工作目录解释。当前兼容入口不会展开参数中的 `${PLUGIN_ROOT}`；使用该占位符会导致 `Cannot find module` 或目录不存在。`env_vars` 是 Codex 的环境变量白名单，只传递本插件的两项配置。

启动器优先使用插件目录的 `bin/betterzip-mcp`。如果首次安装没有二进制，启动器从本项目 Release 下载当前版本并校验 SHA-256。下载信息全部发往 stderr。调试尚未发布的版本时，需要先在原目录构建；指定版本尚无 Release 时自动下载会明确失败。

修改原项目后，可再次执行 `codex plugin add betterzip-mcp@gtkit-local-tools` 刷新已安装副本。正在运行的聊天可能仍持有旧连接；在客户端重新加载 MCP，或重启 Codex 后再使用。仅看到插件已安装，不代表 MCP 握手成功。

v0.1.2 在首次文件操作时通过原生窗口获得用户选择，并仅保存自己的用户授权配置；也可显式运行 `setup`。未配置或用户取消时不使用插件安装目录。插件不会自行修改 Codex 或其他客户端的配置。插件的 `composerIcon` 和 `logo` 均使用随包提供的 `icon.png`，来源为本机 BetterZip 6 应用图标。

## 环境与文件

- macOS；已安装包含 `7za` 的 BetterZip。
- 从源码构建：Go 1.27+，固定依赖 `github.com/mark3labs/mcp-go v1.4.0`。
- npm 或源码 Codex 插件启动：Node.js 20+。**直接运行 Go 二进制不依赖 Node.js 或 Go 运行时。**
- 本 MCP 的基础压缩/解压不收费；BetterZip 自身的许可与授权仍按其厂商条款执行。本项目不代表 MacItBetter。

```text
cmd/main.go                   stdio 入口、配置、只读诊断
internal/betterzipcli.go       7za 调用、超时、取消与输出处理
internal/security.go          路径、归档条目与资源限制
internal/tools.go             四个归档工具及授权配置工具
internal/authorization.go     原生文件夹选择、首次引导与按请求读取授权
go.mod / go.sum               固定 Go 依赖
.codex-plugin/plugin.json     Codex 兼容清单
.mcp.json                     stdio 启动连接
.agents/plugins/marketplace.json
package.json                  npm 分发壳
install.js / launch.js        二进制下载与启动
.env.example                  配置说明，不会被自动加载
icon.png                      本机 BetterZip 6 应用图标
codex-smoke-test.js            通过 Codex 验证持久设置与四工具，报告路径脱敏
internal/settings.go          用户授权目录的读取与原子保存
settings.example.json         用户配置格式示例
```

## 构建

在原项目根目录执行：

```bash
go mod download
mkdir -p bin dist
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/betterzip-mcp ./cmd
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o dist/betterzip-mcp-darwin-arm64 ./cmd
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/betterzip-mcp-darwin-amd64 ./cmd
file dist/betterzip-mcp-darwin-arm64 dist/betterzip-mcp-darwin-amd64
```

两个文件分别供 Apple Silicon 和 Intel 使用，并非单个 fat/universal 文件。`CGO_ENABLED=0` 不要求第三方动态库或动态 Go 运行时；产物仍链接 macOS 自带的 libSystem、libresolv、CoreFoundation 和 Security，并非完全静态的 Mach-O。

## 安全与失败语义

- 所有用户数据读取/写入经 `os.Root` 限定。拒绝越界路径、路径控制字符、符号链接、多硬链接普通文件、设备/FIFO，以及不明确的归档列表。
- 只支持 ZIP/7z。归档中的绝对路径、`..`、反斜杠、冒号、链接、加密条目、重复名称、大小写冲突、文件/目录冲突均拒绝。最多 10000 个条目；技术列表最多 4 MiB；诊断最多 64 KiB。
- 压缩在工作目录内创建独立 0700 临时归档数据，先复制经过验证的源，再运行 7za。这会暂时占用输入副本与输出包所需空间；它是运行数据，不是工程 checkout。原始文件不被修改。通过硬链接独占发布新压缩包，已有目标不覆盖。
- 解压先固定输入包，逐项将 `7za x -so` 的数据写入 Go 打开的文件。7za 不接受解压目标目录；归档路径不能让其向外写文件。实际输出字节必须与条目声明一致。
- 解压目标必须不存在，父目录必须存在。目标目录及内部目录使用 0700，普通文件 0600；不恢复执行位、所有者、ACL、扩展属性或链接。全部条目校验、写入及输出关闭成功后才标记提交；提交前失败清理本次新目录，原本存在的目录不删除。输出目录在执行期间可见，不保证整个目录原子出现。
- 取消通知、客户端断开、SIGINT/SIGTERM 或超时会终止进程组并等待子进程回收。提交前取消会清理本次未完成数据；已提交后到达的取消不会撤销产物。调用者重试前仍应检查目标是否存在。
- 产物已提交但后续资源清理失败时，保留产物，并继续以 MCP 错误结果返回（`isError: true`）；文本明确显示“产物已完成”、相对于授权目录的位置和不要直接重试的提示。Go 的 `Compress` / `Extract` 此时返回非空产物路径及包含清理原因的 error，原有函数签名不变。
- 未提交且清理失败时，不报告成功产物；文本列出无法清理的本次数据相对位置，原始失败和清理失败都保留在错误链中。工具不会自动重试或删除已完成结果。
- 操作期间不要并发修改本次私有临时目录或正在生成的目标。这里防护的是不可信参数/归档，不承诺抵御能够改写本进程、二进制或用户账户的攻击者。
- 所有日志和命令帮助写 stderr，stdout 只承载 MCP JSON-RPC。子进程没有 shell，也不会继承上层进程中的 API Key 环境变量。

## 测试

不需要 Docker，不拉取镜像，不创建容器。

```bash
# 单元测试；显式关闭 go test 默认附带的 vet
go test -vet=off -count=1 ./...
# 真实 BetterZip 7za：ZIP/7z 往返、越界/恶意包、取消与子进程退出
go test -vet=off -count=1 -tags=integration ./...
# 安装器测试不访问 Release，使用受控 HTTP 响应验证失败与校验路径
node --test install.test.js
# 构建本机 bin/betterzip-mcp 后，验证真实 stdio 与 Node 启动链
node --test protocol.test.js
# 验证验收报告脱敏，原始工具结果不被修改
node --test codex-smoke-test.test.js
# 需先安装本地插件；通过真实 Codex MCP 客户端发现并调用全部四个工具
node codex-smoke-test.js
```

`codex-smoke-test.js` 默认使用 PATH 中的 `codex`；可用 `CODEX_BIN` 指定已有 Codex 可执行文件。它在独立 Codex App Server 中创建不保存的内存测试上下文，不发送模型请求，不接管已有聊天。测试使用原项目内的临时用户目录，先运行 `setup`，再验证客户端从保存的设置读取目录，不修改真实用户配置；退出时清理。结果保存在 `dist/codex-mcp-validation.json`，落盘前将用户、项目和测试目录转换为脱敏标识。它验证已安装插件的重载、工具发现、ZIP 往返、两项会员无文件副作用及图标元数据；不代表正在运行的桌面聊天已刷新旧连接。

测试临时文件只生成在原项目内，并由测试清理。测试结果仅代表实际运行的用例；Intel 上的 arm64 交叉构建不能代替 Apple Silicon 实机运行。Cursor/Claude Desktop 的界面内工具发现需要额外实测。

lint、脚本 lint、vet、modern 检查、全量 race 和覆盖率不包含在上述命令中；按项目规则需要功能完成后单独授权。

本次已取得质量检查授权，执行命令如下（不会由构建或安装脚本自动触发）：

```bash
go vet -tags=integration ./...
golangci-lint run --no-config --enable=modernize --build-tags=integration --modules-download-mode=readonly --timeout=3m ./...
govulncheck ./...
go test -race -vet=off -count=1 -timeout=90s -tags=integration ./...
node --test install.test.js protocol.test.js codex-smoke-test.test.js
```

此前质量检查环境：Go 1.27.3、golangci-lint 2.15.0、Intel macOS、BetterZip 6.0.6 随附的 7za 26.03。资源关闭错误已补齐；间接依赖 `golang.org/x/text` 已从 v0.14.0 升至 v0.41.0。该轮 vet、lint（含 modernize）、漏洞扫描和 Go race 单元/集成测试均通过；安装器 8 项、stdio/启动器 4 项通过。取消与错误分支使用受控子进程，ZIP/7z 往返与加密包拒绝使用真实 7za。启动路径修复后，Codex App Server 四工具实际调用也已通过。arm64 尚未在 Apple Silicon 实机运行；Cursor/Claude Desktop UI 未验证，未发布远端。

## 打包与发布教程

以下发布命令仅供维护者手动执行，不会由 MCP Server 或安装器运行。

1. 同步 `package.json`、`.codex-plugin/plugin.json` 与 `internal/betterzipcli.go` 中的版本；维持当前 0.x 主版本，未经明确授权不改为 v2。
2. 按构建步骤生成两个原生文件，确认架构与本机功能测试。
3. 生成 Release 校验文件：

```bash
cd dist
shasum -a 256 betterzip-mcp-darwin-arm64 betterzip-mcp-darwin-amd64 > SHA256SUMS
cd ..
```

4. 在 `gtkit/betterzip-mcp` 发布对应版本（需要维护者已登录 GitHub CLI）：

```bash
gh release create v0.1.2 dist/betterzip-mcp-darwin-arm64 dist/betterzip-mcp-darwin-amd64 dist/SHA256SUMS --repo gtkit/betterzip-mcp --title 'v0.1.2' --generate-notes
```

5. 核对 npm 白名单并打包。包内不得出现 `cmd/`、`internal/`、Go 文件或 `bin/`：

```bash
npm pack --dry-run --ignore-scripts
npm pack --ignore-scripts --pack-destination dist
tar -tzf dist/gtkit-betterzip-mcp-0.1.2.tgz
```

6. Release 可访问后，在已登录且拥有 `@gtkit` 发布权限的环境发布 npm 包：

```bash
npm publish dist/gtkit-betterzip-mcp-0.1.2.tgz --access public
```

包名与下载仓库已配置为 `@gtkit/betterzip-mcp` 与 `gtkit/betterzip-mcp`；实际 npm 名称可用性、组织权限和远端发布仍由维护者确认。当前 package license 为 `UNLICENSED`，不代替权利人授予开源许可。维护者应在发布前明确分发许可；本项目不打包/再分发 BetterZip 的 7za。

## 参考

- [BetterZip 官方 CLI](https://macitbetter.com/library/betterzip/docs/command-line-helper/)
- [Codex 插件打包与市场](https://developers.openai.com/plugins/build/plugins)
- [mcp-go v1.4.0](https://github.com/mark3labs/mcp-go/tree/v1.4.0)
- [open-code-review npm 壳](https://github.com/alibaba/open-code-review/blob/main/package.json)与[安装器](https://github.com/alibaba/open-code-review/blob/main/scripts/install.js)

对齐其“宿主负责推理、确定性工具负责执行”和 npm 下载原生二进制的分工；不复制 OCR 的 LLM 配置、自动更新或平台包机制。

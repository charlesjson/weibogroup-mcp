# WeiboGroup MCP

用 Go 编写的本地 stdio MCP 服务，可接入 LM Studio、DeepSeek Harness 和其他支持 stdio 的客户端。它使用微博群聊归档后台的 AI API，提供与 dsh-weibogroup v0.6.4 对应的 45 个工具。

## 下载

从 [GitHub Releases](https://github.com/charlesjson/weibogroup-mcp/releases) 下载对应系统和处理器的压缩包，解压即可使用。Linux 和 macOS 可运行以下命令安装到用户目录。Windows 可将 weibogroup-mcp.exe 放入自己的程序目录。

```sh
install -Dm755 weibogroup-mcp "$HOME/.local/bin/weibogroup-mcp"  # Linux
"$HOME/.local/bin/weibogroup-mcp" --version
```

macOS 可使用 `mkdir -p ~/.local/bin && install -m755 weibogroup-mcp ~/.local/bin/`。运行二进制无需安装 Go 或 Node.js。

## 配置端点和 API Key

在 WeiboGroup 后台的 **AI API Tokens** 页面创建密钥。查询和下载需要 `api:read`，创建导出、修改监控和管理采集任务需要 `api:write`。端点可填写 Viewer 根地址或完整的 `/api/v1` 根路径，也支持反向代理的子路径。远程端点使用 HTTPS，本机回环地址可使用 HTTP。

程序按以下顺序读取一整对端点和密钥。

1. 命令行参数。密钥作为参数可能出现在本机进程列表中。
2. 环境变量 `WEIBOGROUP_API_ENDPOINT` 和 `WEIBOGROUP_API_KEY`。
3. 原 DSH 插件的 `~/.config/dsh-weibogroup/connection.json`。可用 `WEIBOGROUP_CONNECTION_FILE` 自定义路径。每次工具调用都会重读这个文件，Unix 文件权限设为 `600`。

```sh
weibogroup-mcp --api-endpoint 'https://archive.example.com/api/v1' --api-key "$ARCHIVE_API_KEY"
```

已有 DSH 连接文件可直接复用，格式如下。

```json
{
  "baseUrl": "https://archive.example.com",
  "apiToken": "<后台签发的 wg_ai_ 密钥>"
}
```

## 接入 LM Studio

在 Program → Install → Edit mcp.json 中添加下列配置，把 command 改为解压后程序的绝对路径，并替换端点和密钥。如使用已有连接文件，可省略 env。启动后先试“列出我归档的群”。[LM Studio 配置文档](https://lmstudio.ai/docs/app/mcp)

```json
{
  "mcpServers": {
    "weibogroup": {
      "command": "/absolute/path/to/weibogroup-mcp",
      "env": {
        "WEIBOGROUP_API_ENDPOINT": "https://archive.example.com/api/v1",
        "WEIBOGROUP_API_KEY": "<后台签发的 wg_ai_ 密钥>"
      }
    }
  }
}
```

## 接入 DeepSeek Harness

先将二进制放入 **DSH 进程的 PATH**，预备好上述连接文件，然后安装此仓库提供的插件配置并重启对应 profile。

```sh
dsh plugin --profile web add github:charlesjson/weibogroup-mcp#v0.1.2
```

也可以在 DSH 的 MCP 设置中新增 stdio 服务，将 command 设为二进制的绝对路径，serverName 设为 weibogroup。桌面应用的 PATH 可能与终端不同。DSH 工具显示为 `mcp__weibogroup__weibo_listGroups` 等名称。

## 工具与文件

- 账号、群和成员发现，历史消息搜索、上下文、时间线、关键词与发言统计。消息页默认 10 条，最多 20 条和 24,000 字节。搜索续用 nextCursor，上下文续用 nextPageCursor，窗口续用 pagination.continuation。
- 图片工具接收消息中的资源标识，校验绑定和 SHA-256 后返回 MCP 图片内容。图片上限 20 MiB，需要客户端和模型支持图片输入。
- 导出工具复用后台消息导出（CSV、XLSX、JSON）和个人阅读版导出（Markdown+图片 ZIP）。历史 PDF 任务在有效期内仍可查询和下载。任务 ready 后调用 weibo_downloadExport，文件默认保存到 ~/Downloads/weibogroup 下的独立目录。可用 WEIBOGROUP_EXPORT_DIR 指定目录。返回值含绝对路径、字节数和覆盖信息，单文件上限 64 MiB。
- 监控规则、命中记录、通知投递和诊断，以及历史采集任务的创建、暂停、恢复、取消和重试。可用权限以当前 Token 为准。

## 从源码构建

```sh
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o dist/weibogroup-mcp .
```

Go 版本要求见 go.mod。跨平台打包使用 scripts/release.sh。协议集成测试使用 Node.js 22.19+ 和独立的 TypeScript MCP 客户端，运行 `npm ci && npm test`。更新工具契约时，在指定版本的 dsh-weibogroup 仓库完成构建，再运行 `node scripts/sync-catalog.mjs /path/to/dsh-weibogroup`。

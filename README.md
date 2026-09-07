# S3M CLI (S3 Mini Client)

S3M（S3 Mini Client）是 S3 对象存储协议的命令行客户端，支持命令行模式和 TUI 交互模式。

## 功能特性

- **命令行模式**: 单次执行操作，适合脚本和自动化
- **TUI 交互模式**: 基于 Bubble Tea 的终端用户界面，支持分屏布局、进度显示、历史记录
- **多 context 管理**: 支持多个 S3 服务端 context，可在运行时切换
- **加密存储**: 敏感信息（accesskey/secretkey 合并）加密存储，更安全
- **机器绑定**: 加密密钥基于本机 MAC/主机名/用户名派生，配置文件无法在其他机器解密

## 安装

```bash
go build -o s3m
```

## Context 管理

S3M 使用 context 管理多个 S3 服务端。每个 context 保存一个服务端的 `endpoint / usessl / accesskey / secretkey`，其中 accesskey/secretkey 整体加密存储。

### 快速开始

```bash
# 创建第一个 context（交互式）
s3m context set prod
# 按提示输入 Endpoint / AccessKey / SecretKey / UseSSL

# 创建第二个 context
s3m context set dev

# 查看所有 context
s3m context list
#   dev                   10.0.0.1:9000
# * prod                 (current)  s3.example.com

# 设置默认 context
s3m context current prod

# 临时使用某个 context（仅本次进程）
s3m --context dev list my-bucket/photos/

# 启动 TUI 时使用某个 context
s3m --context prod
```

### Context 子命令

| 命令 | 说明 |
|------|------|
| `s3m context` / `s3m context list` | 列出所有 context |
| `s3m context current` | 显示当前默认 context |
| `s3m context current <name>` | 设置默认 context（落盘） |
| `s3m context use <name>` | 设置默认 context（CLI 模式下等同于 `current`） |
| `s3m context set-default <name>` | 设置默认 context（落盘） |
| `s3m context show [name]` | 解密显示 context 详情 |
| `s3m context set <name>` | 交互式创建/更新 context |
| `s3m context rename <old> <new>` | 重命名 context |
| `s3m context delete <name> [-f]` | 删除 context（删除当前时自动清空 current-context） |
| `s3m context import <file>` | 从明文 conf 文件导入 context 到默认配置 |

### 配置文件

配置文件保存到 `~/.config/s3m/s3m.conf`，格式示例：

```ini
# S3M contexts
current-context=prod

[prod]
ctx.prod.endpoint=s3.example.com
ctx.prod.usessl=true
ctx.prod.auth=enc:aes:<密文>

[dev]
ctx.dev.endpoint=10.0.0.1:9000
ctx.dev.usessl=false
ctx.dev.auth=enc:aes:<密文>
```

`ctx.<name>.auth` 的明文格式为 `<accessKey>\x1f<secretKey>`（`\x1f` 是 ASCII Unit Separator），整体 AES-256-GCM 加密后用 base64 编码，前缀 `enc:aes:`。

### 配置文件查找优先级

1. `--config` 参数指定的路径
2. 当前目录 `./s3m.conf`
3. 用户目录 `~/.config/s3m/s3m.conf`
4. 系统目录 `/etc/s3m/s3m.conf`

### `--config` 明文 conf（只读模式）

通过 `--config=path/to/s3m.conf` 指定外部配置文件时：

- **允许明文 AK/SK**：不必提前加密，方便临时访问某个 endpoint
- **不触发迁移**：原文件保持不动
- **写操作禁止**：`context set/upsert/rename/delete/set-default` 全部报错并提示
- **TUI 仍可读**：标题栏显示 `[ctx: name (readonly)]`，`use <name>` 临时切换可用，`set-default` 被拒

支持的明文 conf 格式：

**新格式（多 context，扁平 key=value）**：

```ini
current-context=dev

[dev]
ctx.dev.endpoint=10.0.0.1:9000
ctx.dev.usessl=false
# auth 明文格式：<ak>\x1f<sk>（\x1f 是 ASCII Unit Separator）
ctx.dev.auth=AKDEV\x1fSKDEV
```

**旧格式（单 context，触发自动迁移被禁用）**：

```ini
endpoint=10.0.0.1:9000
usessl=false
accesskey=AKDEV
secretkey=SKDEV
```

密文格式（`enc:aes:...`）也仍然兼容，可与明文混存于同一 conf。

使用示例：

```bash
# 临时访问一个 endpoint
s3m --config=/tmp/my-plain.conf list my-bucket/photos/

# 明文 conf 写操作会被拒绝
s3m --config=/tmp/my-plain.conf context set foo
# 错误: 配置文件 /tmp/my-plain.conf 是只读模式（明文 conf），不允许写入
# 提示: 取消 --config 参数使用默认路径 ~/.config/s3m/s3m.conf 来管理加密 context
```

### 从明文 conf 导入 context

可以把明文 conf 中的 context 合并到默认配置中：

```bash
# 从明文 conf 导入：同名覆盖、默认独有保留、导入文件不变
s3m context import /tmp/my-plain.conf
# 已从 /tmp/my-plain.conf 导入 2 个 context: dev, staging
```

合并规则：
- 导入文件中的 context 与默认配置同名 → 覆盖默认配置
- 默认配置独有的 context → 保留
- 导入文件的 `current-context` 不会同步到默认配置（避免误改当前默认）
- 导入文件本身**不会**被修改
- 导入文件不存在时返回错误并退出码 1

适用场景：把团队共享的明文 conf、CI 临时 conf、minio server 导出的 conf 等批量导入到本地默认配置。

### 旧格式兼容

如果 `s3m.conf` 中只包含 `endpoint=.../usessl=.../accesskey=.../secretkey=...` 的旧格式（无 `ctx.*` 段），S3M 在加载时自动迁移为新格式：
- 把 AK/SK 合并加密写入 `ctx.default.auth`
- 设置 `current-context=default`
- 原文件备份为 `s3m.conf.bak`

### Context 解析优先级

- `--context <name>` 参数 > `current-context` 字段
- 都没有时报错并提示创建

### TUI 中的 context 切换

- 启动时通过 `--context <name>` 指定，或使用 `current-context`
- TUI 标题栏显示当前 context / endpoint：`s3m v0.1.0  Profile: prod  Endpoint: s3.example.com`
- 命令模式新增：
  - `use <name>`：临时切换（不落盘，退出后下次启动仍是原默认）
  - `set-default <name>`：切换并落盘（修改 current-context）
- TUI 中使用 `:` 进入命令模式，执行 `use <name>` / `set-default <name>`

## 使用方式

### TUI 交互模式

直接运行 `s3m` 进入 TUI 界面:

```bash
./s3m
```

**界面布局：**

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ s3m v0.1.0  Profile: default  Endpoint: s3.example.com               ? Help │
├──────────────────────────────────────────────────────────────────────────────┤
│ Buckets (3)             │ Objects: my-bucket / projects/                    │
│ ▶ my-bucket             │ ← ..                                              │
│ ○ dev-assets            │ 📂 2024-08-20 10:00      0 B  images              │
│ ○ logs                  │ 📄 2024-08-23 08:12   12.3 KiB style.css          │
│                         │ [2/47]  Filter: css                               │
├──────────────────────────────────────────────────────────────────────────────┤
│ [i] Meta │ [v] 预览 │ [u] 上传 │ [d] 下载 │ [x] 删除 │ [Space] 多选 │ [Tab] │
│ Bucket: my-bucket │ Prefix: projects/ │ Total: 47 objects │ ctx: default    │
└──────────────────────────────────────────────────────────────────────────────┘
```

- 左侧 20% 为桶列表，右侧 80% 为对象浏览区
- 标题栏显示版本、当前 context、S3 endpoint 与帮助入口
- 对象区支持目录导航、过滤、Meta 弹窗、文本预览、上传/下载、删除、多选
- 单文件上传/下载会在对象区底部显示真实字节进度、速率与 ETA
- 终端低于 `80x24` 时会提示窗口过小

**快捷键:**

| 快捷键 | 说明 |
|--------|------|
| `Tab` | 切换桶面板 / 对象面板焦点 |
| `j` `k` / `↑` `↓` | 上下移动 |
| `g` / `G` | 跳到顶部 / 底部 |
| `Ctrl+D` / `Ctrl+U` | 向下 / 向上翻半页 |
| `/` | 进入过滤模式，实时过滤对象名 |
| `n` / `N` | 跳到下一个 / 上一个过滤结果 |
| `?` | 打开帮助弹窗 |
| `:` | 进入命令模式 |
| `q` | 退出确认 |

**桶面板：**

| 快捷键 | 说明 |
|--------|------|
| `Enter` / `l` / `→` | 进入桶 |
| `r` | 刷新桶列表 |

**对象面板：**

| 快捷键 | 说明 |
|--------|------|
| `Enter` / `l` / `→` | 进入目录 / 预览文本文件 |
| `h` / `←` / `Backspace` | 返回上一级前缀 |
| `~` | 回到桶根目录 |
| `i` | 查看对象 Meta 信息 |
| `v` | 预览文本文件 |
| `u` / `U` | 上传文件 / 上传整个目录 |
| `d` / `D` | 下载对象 / 递归下载目录 |
| `x` / `X` | 删除当前对象 / 批量删除已选择对象 |
| `Space` | 多选切换 |
| `c` | 复制当前对象完整 Key 到剪贴板 |
| `r` | 刷新当前对象列表 |

**预览模式：**

| 快捷键 | 说明 |
|--------|------|
| `Esc` / `q` | 关闭预览 |
| `j` / `k` | 上下滚动 |
| `Ctrl+D` / `Ctrl+U` | 翻页 |
| `w` | 切换自动换行 |

**命令模式命令:**

| 命令 | 说明 | 示例 |
|------|------|------|
| `cd <path>` | 跳转桶或前缀，`cd ..` 返回上一级 | `cd my-bucket`, `cd photos/` |
| `ls` / `list` | 刷新当前列表 | `ls` |
| `get <object>` | 下载对象到当前目录 | `get file.txt` |
| `put <file> [object]` | 上传文件到当前前缀 | `put ./local.txt`, `put ./a.txt notes/a.txt` |
| `sign <object>` | 生成预签名链接 | `sign file.txt` |
| `use <name>` | 临时切换 context | `use dev` |
| `set-default <name>` | 切换并落盘默认 context | `set-default prod` |
| `history` | 查看操作历史弹窗 | `history` |
| `help` | 打开帮助弹窗 | `help` |
| `exit` / `quit` / `q` | 退出 TUI | `quit` |

### 命令行模式

#### 列出对象

```bash
s3m list bucket/prefix              # 列出根级对象
s3m list bucket/photos/             # 列出指定前缀下的对象
s3m list bucket/ -r                 # 递归列出所有对象
s3m list bucket/photos/ -r          # 递归列出指定前缀下所有对象
```

#### 查询对象元数据

```bash
s3m stat bucket/object              # 获取对象详细信息
s3m stat my-bucket/photos/2024/image.jpg
```

#### 下载对象

```bash
s3m get bucket/object               # 下载对象（默认保存为对象名）
s3m get bucket/object -o /tmp/file  # 指定保存路径

# 递归下载目录
s3m get bucket/photos/ ./local-photos/ -r      # 逐个下载
s3m get bucket/docs/ ./docs/ -r -c 5           # 5个并发下载
```

#### 输出对象内容

```bash
s3m cat bucket/object               # 输出对象内容到 stdout
s3m cat my-bucket/logs/app.log
```

#### 上传文件

```bash
s3m put bucket/object local-file    # 上传文件
s3m put bucket/data.json ./data.json -t application/json  # 指定 Content-Type

# 递归上传目录
s3m put bucket/photos/ ./local-photos/ -r      # 逐个上传
s3m put bucket/docs/ ./docs/ -r -c 5           # 5个并发上传
```

#### 生成签名下载链接

```bash
s3m sign bucket/object              # 生成签名链接（默认 7 天有效）
s3m sign bucket/object -e 24h       # 指定 24 小时有效
s3m sign bucket/object -e 1h        # 指定 1 小时有效
```

#### 复制对象

同一 context 内复制（服务端复制，数据不经过本机）：

```bash
s3m copy src-bucket/src-object dest-bucket/dest-object  # 复制单个对象
s3m copy bucket1/file.txt bucket2/backup/file.txt       # 示例

# 递归复制目录
s3m copy bucket1/photos/ bucket2/backup/photos/ -r      # 逐个复制
s3m copy bucket1/photos/ bucket2/backup/photos/ -r -c 5 # 5个并发复制

# 大文件分片复制（用于超过 5GB 的文件）
s3m copy bucket1/large.dat bucket2/large-copy.dat -b
s3m copy bucket1/large.dat bucket2/large-copy.dat --big
```

#### 跨 context 复制对象

跨服务端复制通过 `--to-context` 指定目标 context，`--context` 指定源 context：

```bash
# 源使用当前 context，目标使用 dev context
s3m copy bucket1/file.txt --to-context dev bucket2/file.txt

# 源使用 prod context，目标使用 dev context
s3m copy --context prod bucket1/file.txt --to-context dev bucket2/file.txt

# 跨服务端递归复制目录
s3m copy bucket1/photos/ --to-context dev bucket2/photos/ -r
s3m copy bucket1/photos/ --to-context dev bucket2/photos/ -r -c 5
```

省略 `--context` 时源端使用 current-context；省略 `--to-context` 时目标端与源端相同（服务端复制）。

工作原理与限制：

S3 的服务端复制（`x-amz-copy-source`）只能在同一 endpoint 内进行，
因此跨 context 复制必须经本机流式中转（源端 `GetObject` → 目标端 `PutObject`），
数据不落盘、内存占用有界。由此带来以下差异：

- 数据经过本机，速度受本机上下行带宽限制
- 仅保留 `Content-Type`，**不复制**自定义元数据、存储类别、标签、ACL
- 只校验对象大小，不校验 ETag（跨 S3 实现 ETag 算法不可比）
- 忽略 `-b`，分片由流式上传自动处理（会打印提示）
- 失败需整个对象重传，不支持续传

是否走跨端路径按 **context 名**判定：`--to-context` 未指定或与源 context 同名时走服务端复制，
否则走流式中转。即使两个 context 指向同一 endpoint 也会走流式中转，
这样可避免用目标端凭据读取源端 bucket 导致的权限失败。

#### 删除对象

```bash
s3m del bucket/object               # 删除单个对象（需确认）
s3m del bucket/object --force       # 删除单个对象（无需确认）

# 递归删除目录
s3m del bucket/photos/ -r                # 逐个删除（需确认）
s3m del bucket/photos/ -r -c 5           # 5个并发删除（需确认）
s3m del bucket/photos/ -r --force        # 逐个删除（无需确认）
s3m del bucket/photos/ -r -c 5 --force   # 5个并发删除（无需确认）
```

**删除确认提示：**
- 执行删除前会提示 `确定要删除 xxx 吗？(y/N)`
- 输入 `y` 或 `Y` 确认删除
- 输入其他任何内容取消操作
- 使用 `--force` 选项跳过确认直接删除

### Context 管理

```bash
s3m context                              # 列出所有 context
s3m context list                         # 同上
s3m context current                      # 显示当前 context
s3m context current <name>               # 设置默认 context
s3m context set <name>                   # 交互式创建/更新 context
s3m context show [name]                  # 解密显示 context 详情
s3m context rename <old> <new>           # 重命名 context
s3m context delete <name> [-f]           # 删除 context
```

运行时使用 `--context` 临时指定，TUI 中通过 `use`/`set-default` 切换。详见上节 "Context 管理"。

## 项目结构

```
s3m/
├── main.go                # 程序入口
├── config.go              # 配置文件解析、ContextStore、旧格式迁移
├── context_ops.go         # main 包注入给 commands 的 context 操作实现
├── client.go              # S3M 客户端创建
├── crypto/
│   └── crypto.go          # 加解密（EncryptCredentials/DecryptCredentials）
├── commands/
│   ├── root.go            # Cobra 命令定义
│   └── context.go         # context 子命令
├── operations/
│   ├── types.go           # 数据结构定义
│   ├── list.go            # 列出对象
│   ├── stat.go            # 查询元数据
│   ├── get.go             # 下载对象
│   ├── cat.go             # 输出内容
│   ├── put.go             # 上传文件
│   ├── copy.go            # 复制对象（服务端复制、分片复制、跨 context 流式复制）
│   ├── del.go             # 删除对象
│   └── presign.go         # 生成签名 URL
├── tui/
│   ├── model.go           # TUI 状态模型
│   ├── app.go             # Bubble Tea 主程序
│   └── styles.go          # 样式定义
└── README.md              # 说明文档
```

## 依赖

- [Cobra](https://github.com/spf13/cobra) - 命令行解析
- [minio-go](https://github.com/minio/minio-go) - MinIO/S3 SDK
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - TUI 框架
- [golang.org/x/crypto](https://golang.org/x/crypto) - PBKDF2 密钥派生

## 许可证

MIT

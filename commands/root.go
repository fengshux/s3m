package commands

import (
	"fmt"
	"os"
	"strings"
	"time"

	"s3m/operations"
	"s3m/tui"

	"github.com/minio/minio-go/v7"
	"github.com/spf13/cobra"
)

// parseBucketPath 解析 bucket/path 格式的参数
// 返回 bucket 和 path（path 可能为空）
func parseBucketPath(bucketPath string) (bucket, path string, err error) {
	parts := strings.SplitN(bucketPath, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", fmt.Errorf("无效的路径格式: %s，应为 bucket/path 或 bucket", bucketPath)
	}
	bucket = parts[0]
	if len(parts) > 1 {
		path = parts[1]
	}
	return bucket, path, nil
}

// requirePath 验证 path 必须非空，用于需要 objectKey 的命令
func requirePath(path, cmdName string) error {
	if path == "" {
		return fmt.Errorf("%s 命令需要指定对象路径，格式: bucket/object", cmdName)
	}
	return nil
}

var (
	configPath  string
	contextName string
	debug       bool
	client      *minio.Client
	core        *minio.Core
	usedContext string
)

// ClientWrapper 包装 minio.Client 以便在 main 包中初始化
type ClientWrapper struct {
	Client *minio.Client
	Core   *minio.Core
}

// InitClient 由 main 包设置的初始化客户端回调函数
// 返回 ClientWrapper、实际使用的 context 名称、错误
var InitClient func(configPath, contextName string, debug bool) (*ClientWrapper, string, error)

// clientForContext 返回指定 context 的 client。
// name 为空或等于当前已初始化的 context 时，复用已有 client，避免重复建连。
func clientForContext(name string) (*minio.Client, *minio.Core, error) {
	if name == "" || name == usedContext {
		return client, core, nil
	}
	wrapper, _, err := InitClient(configPath, name, debug)
	if err != nil {
		return nil, nil, fmt.Errorf("初始化 context %q 失败: %w", name, err)
	}
	return wrapper.Client, wrapper.Core, nil
}

// NewRootCmd 创建根命令
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "s3m",
		Short: "S3 Mini Client：S3 对象存储协议命令行客户端",
		Long: `S3M（S3 Mini Client）是 S3 对象存储协议的命令行客户端，支持列出、查询、下载对象等操作

Context 管理:
  使用 's3m context' 子命令管理多个服务端。
  启动时可通过 --context 指定 context，否则使用 current-context。
  每个 context 的 accesskey/secretkey 加密存储在配置文件中。`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// context 子命令不创建 client，但需要 --config/--context 参数生效
			if cmd.Name() == "context" || (cmd.Parent() != nil && cmd.Parent().Name() == "context") {
				return nil
			}
			wrapper, used, err := InitClient(configPath, contextName, debug)
			if err != nil {
				return err
			}
			client = wrapper.Client
			core = wrapper.Core
			usedContext = used
			return nil
		},
		Run: func(cmd *cobra.Command, args []string) {
			// 无子命令时进入 TUI 交互模式
			onContextChange := func(name string) (*minio.Client, *minio.Core, error) {
				wrapper, used, err := InitClient(configPath, name, debug)
				if err != nil {
					return nil, nil, err
				}
				usedContext = used
				return wrapper.Client, wrapper.Core, nil
			}
			tui.PersistCurrentContext = CtxOps.SetCurrentFn
			readOnly := CtxOps.ReadOnlyFn != nil && CtxOps.ReadOnlyFn()
			if err := tui.Run(client, usedContext, onContextChange, readOnly); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		},
	}

	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "配置文件路径（默认: ./s3m.conf, ~/.config/s3m/s3m.conf, /etc/s3m/s3m.conf）")
	rootCmd.PersistentFlags().StringVar(&contextName, "context", "", "指定 context 名称（默认: current-context）")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "启用 HTTP 请求调试输出")

	// 添加子命令
	rootCmd.AddCommand(
		NewContextCmd(),
		NewPutCmd(),
		NewListCmd(),
		NewStatCmd(),
		NewGetCmd(),
		NewCatCmd(),
		NewSignCmd(),
		NewCopyCmd(),
		NewDelCmd(),
	)

	return rootCmd
}

// NewListCmd 创建 list 子命令
func NewListCmd() *cobra.Command {
	var recursive bool
	cmd := &cobra.Command{
		Use:     "list bucket/prefix",
		Aliases: []string{"ls"},
		Short:   "列出存储桶中的对象",
		Long: `列出指定存储桶中的所有对象，可选择按前缀过滤和递归列出

参数:
  bucket/prefix  存储桶名称和可选的对象前缀（bucket/ 或 bucket/path/）

示例:
  s3m list my-bucket/                # 列出根级对象
  s3m list my-bucket/photos/         # 列出 photos/ 前缀下的对象
  s3m list my-bucket/ -r             # 递归列出所有对象
  s3m list my-bucket/photos/ -r      # 递归列出 photos/ 下所有对象`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			bucket, prefix, err := parseBucketPath(args[0])
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			lister := operations.NewLister(client)
			lister.List(cmd.Context(), bucket, prefix, recursive)
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "递归列出对象")
	return cmd
}

// NewStatCmd 创建 stat 子命令
func NewStatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stat bucket/object",
		Short: "查询对象元数据",
		Long: `获取指定对象的详细信息，包括大小、ETag、最后修改时间、Content-Type 等

参数:
  bucket/object  存储桶名称和对象路径

示例:
  s3m stat my-bucket/file.txt
  s3m stat my-bucket/photos/2024/image.jpg

输出格式:
  对象名: <key>
  大小: <MB> (<字节>)
  ETag: <etag>
  最后修改时间: <timestamp>
  Content-Type: <type>`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			bucket, objectName, err := parseBucketPath(args[0])
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			if err := requirePath(objectName, "stat"); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			stater := operations.NewStater(client)
			stater.Stat(cmd.Context(), bucket, objectName)
		},
	}
}

// NewGetCmd 创建 get 子命令
func NewGetCmd() *cobra.Command {
	var output string
	var recursive bool
	var concurrent int
	cmd := &cobra.Command{
		Use:   "get bucket/object [local-dir]",
		Short: "下载对象或目录到本地",
		Long: `从存储桶下载指定对象或目录到本地

参数:
  bucket/object  存储桶名称和对象路径或目录前缀
  local-dir      本地保存目录（仅递归下载时使用，默认: 当前目录）

选项:
  -o, --output     本地保存路径（下载单个对象时使用）
  -r, --recursive  递归下载整个目录
  -c, --concurrent 并发下载数量（仅与 -r 一起使用，默认 0 表示逐个下载）

示例:
  # 下载单个对象
  s3m get my-bucket/file.txt                             # 保存为 file.txt
  s3m get my-bucket/photos/image.jpg -o /tmp/photo.jpg   # 指定路径

  # 递归下载目录
  s3m get my-bucket/photos/ ./local-photos/ -r           # 逐个下载
  s3m get my-bucket/docs/ ./docs/ -r -c 5                # 5个并发下载`,
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			bucket, objectName, err := parseBucketPath(args[0])
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			if err := requirePath(objectName, "get"); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}

			getter := operations.NewGetter(client)

			if recursive {
				// 递归下载目录
				localDir := "."
				if len(args) > 1 {
					localDir = args[1]
				}
				getter.GetDir(cmd.Context(), bucket, objectName, localDir, concurrent)
			} else {
				// 下载单个对象
				getter.Get(cmd.Context(), bucket, objectName, output)
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "本地保存路径（下载单个对象时使用）")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "递归下载整个目录")
	cmd.Flags().IntVarP(&concurrent, "concurrent", "c", 0, "并发下载数量（仅与 -r 一起使用）")
	return cmd
}

// NewCatCmd 创建 cat 子命令
func NewCatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cat bucket/object",
		Short: "输出对象内容到控制台",
		Long: `将指定对象的内容直接输出到标准输出

参数:
  bucket/object  存储桶名称和对象路径

示例:
  s3m cat my-bucket/file.txt
  s3m cat my-bucket/logs/app.log`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			bucket, objectName, err := parseBucketPath(args[0])
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			if err := requirePath(objectName, "cat"); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			catter := operations.NewCatter(client)
			catter.Cat(cmd.Context(), bucket, objectName)
		},
	}
}

// NewPutCmd 创建 put 子命令
func NewPutCmd() *cobra.Command {
	var contentType string
	var recursive bool
	var concurrent int
	cmd := &cobra.Command{
		Use:   "put bucket/object local-file",
		Short: "上传本地文件或目录到存储桶",
		Long: `将本地文件或目录上传到指定存储桶

参数:
  bucket/object  存储桶名称和对象存储路径（或目录前缀，与 -r 一起使用）
  local-file     本地文件路径或目录路径（与 -r 一起使用）

选项:
  -t, --type       Content-Type（默认: 自动检测，仅单文件上传时有效）
  -r, --recursive  递归上传整个目录
  -c, --concurrent 并发上传数量（仅与 -r 一起使用，默认 0 表示逐个上传）

示例:
  s3m put my-bucket/file.txt ./local.txt
  s3m put my-bucket/photos/image.jpg ./photo.jpg
  s3m put my-bucket/data.json ./data.json -t application/json
  s3m put my-bucket/photos/ ./local-photos/ -r           # 递归上传目录
  s3m put my-bucket/docs/ ./docs/ -r -c 5                # 5个并发上传`,
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			bucket, objectName, err := parseBucketPath(args[0])
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			if err := requirePath(objectName, "put"); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			localPath := args[1]
			putter := operations.NewPutter(client)

			if recursive {
				putter.PutDir(cmd.Context(), bucket, objectName, localPath, concurrent)
			} else {
				putter.Put(cmd.Context(), bucket, objectName, localPath, contentType)
			}
		},
	}
	cmd.Flags().StringVarP(&contentType, "type", "t", "", "Content-Type（默认: 自动检测）")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "递归上传整个目录")
	cmd.Flags().IntVarP(&concurrent, "concurrent", "c", 0, "并发上传数量（仅与 -r 一起使用）")
	return cmd
}

// NewSignCmd 创建 sign 子命令
func NewSignCmd() *cobra.Command {
	var expire time.Duration
	cmd := &cobra.Command{
		Use:   "sign bucket/object",
		Short: "生成带签名的下载链接",
		Long: `为指定对象生成带签名的 HTTP 下载链接

参数:
  bucket/object  存储桶名称和对象路径

选项:
  -e, --expire  链接有效期（默认: 168h 即 7 天）

示例:
  s3m sign my-bucket/file.txt
  s3m sign my-bucket/photos/image.jpg -e 24h
  s3m sign my-bucket/data.zip -e 1h`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			bucket, objectName, err := parseBucketPath(args[0])
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			if err := requirePath(objectName, "sign"); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			signer := operations.NewSigner(client)
			signer.Sign(cmd.Context(), bucket, objectName, expire)
		},
	}
	cmd.Flags().DurationVarP(&expire, "expire", "e", 7*24*time.Hour, "链接有效期（如: 1h, 24h, 168h）")
	return cmd
}

// NewCopyCmd 创建 copy 子命令
func NewCopyCmd() *cobra.Command {
	var recursive bool
	var concurrent int
	var bigFile bool
	var toContext string
	cmd := &cobra.Command{
		Use:   "copy src-bucket/src-object dest-bucket/dest-object",
		Short: "复制对象或目录（支持跨存储桶、跨 context）",
		Long: `将对象或目录从一个位置复制到另一个位置，支持跨存储桶复制和跨 context（跨服务端）复制

参数:
  src-bucket/src-object   源存储桶和对象名称或目录前缀
  dest-bucket/dest-object 目标存储桶和对象名称或目录前缀

选项:
  -r, --recursive     递归复制整个目录
  -c, --concurrent    并发复制数量（仅与 -r 一起使用，默认 0 表示逐个复制）
  -b, --big           大文件分片复制（用于超过 5GB 的文件，仅同 context 有效）
      --to-context    目标 context 名称（跨 context 复制时指定，省略时使用源 context）
      --context       源 context 名称（省略时使用 current-context）

同 context 复制（服务端复制，数据不经过本机）:
  s3m copy my-bucket/file.txt my-bucket/copy.txt              # 复制单个对象
  s3m copy bucket1/photos/ bucket2/backup/photos/ -r          # 递归复制目录（逐个）
  s3m copy bucket1/photos/ bucket2/backup/photos/ -r -c 5     # 递归复制目录（5个并发）
  s3m copy bucket1/large.dat bucket2/large-copy.dat -b        # 大文件分片复制

跨 context 复制（流式中转，数据经过本机）:
  s3m copy bucket1/file.txt --to-context dev bucket2/file.txt              # 源用当前 context
  s3m copy --context prod bucket1/file.txt --to-context dev bucket2/file.txt
  s3m copy bucket1/photos/ --to-context dev bucket2/photos/ -r -c 5        # 跨服务端递归复制

跨 context 限制:
  - 仅保留 Content-Type，不复制自定义元数据、存储类别、标签、ACL
  - 只校验对象大小，不校验 ETag（跨 S3 实现 ETag 算法不可比）
  - 忽略 -b，分片由流式上传自动处理
  - 失败需整个对象重传，不支持续传`,
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			srcBucket, srcObject, err := parseBucketPath(args[0])
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			destBucket, destObject, err := parseBucketPath(args[1])
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			if err := requirePath(srcObject, "copy (源)"); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			if err := requirePath(destObject, "copy (目标)"); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}

			// 源 context：--context 指定，否则使用 current（usedContext）。
			srcCtx := usedContext
			if contextName != "" {
				srcCtx = contextName
			}

			// 按 context 名判定是否跨端。不比对 endpoint：
			// 同 endpoint 不同凭据时，服务端复制会用目标凭据去读源 bucket，可能因权限失败。
			if toContext == "" || toContext == srcCtx {
				// 未指定 --to-context，或与源 context 同名，走服务端复制。
				// 注意必须按 srcCtx 取 client，因为它可能不是当前 context。
				sameClient, sameCore, err := clientForContext(srcCtx)
				if err != nil {
					fmt.Println(err)
					os.Exit(1)
				}
				copier := operations.NewCopier(sameClient, sameCore)
				switch {
				case recursive:
					copier.CopyDir(cmd.Context(), srcBucket, srcObject, destBucket, destObject, concurrent)
				case bigFile:
					copier.MultipartCopy(cmd.Context(), srcBucket, srcObject, destBucket, destObject)
				default:
					copier.Copy(cmd.Context(), srcBucket, srcObject, destBucket, destObject)
				}
				return
			}

			// 跨 context：流式中转
			srcClient, _, err := clientForContext(srcCtx)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			destClient, _, err := clientForContext(toContext)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}

			if bigFile {
				fmt.Println("提示: 跨 context 复制忽略 -b，分片由流式上传自动处理")
			}

			crossCopier := operations.NewCrossCopier(srcClient, destClient)
			if recursive {
				crossCopier.CrossCopyDir(cmd.Context(), srcBucket, srcObject, destBucket, destObject, concurrent)
			} else {
				crossCopier.CrossCopy(cmd.Context(), srcBucket, srcObject, destBucket, destObject)
			}
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "递归复制整个目录")
	cmd.Flags().IntVarP(&concurrent, "concurrent", "c", 0, "并发复制数量（仅与 -r 一起使用）")
	cmd.Flags().BoolVarP(&bigFile, "big", "b", false, "大文件分片复制（仅同 context 有效）")
	cmd.Flags().StringVar(&toContext, "to-context", "", "目标 context 名称（跨 context 复制时指定，省略时使用源 context）")
	return cmd
}

// NewDelCmd 创建 del 子命令
func NewDelCmd() *cobra.Command {
	var recursive bool
	var force bool
	var concurrent int
	cmd := &cobra.Command{
		Use:   "del bucket/object",
		Short: "删除对象或目录",
		Long: `删除指定存储桶中的对象或目录

参数:
  bucket/object  存储桶名称和对象名称或目录前缀

选项:
  -r, --recursive    递归删除整个目录
  -c, --concurrent   并发删除数量（仅与 -r 一起使用，默认 0 表示逐个删除）
  --force            强制删除，不进行确认提示

示例:
  s3m del my-bucket/file.txt                  # 删除单个对象（需确认）
  s3m del my-bucket/file.txt --force          # 删除单个对象（无需确认）
  s3m del my-bucket/photos/ -r                # 递归删除目录（逐个，需确认）
  s3m del my-bucket/photos/ -r -c 5           # 递归删除目录（5个并发，需确认）
  s3m del my-bucket/photos/ -r --force        # 递归删除目录（逐个，无需确认）
  s3m del my-bucket/photos/ -r -c 5 --force   # 递归删除目录（5个并发，无需确认）

确认提示:
  执行删除前会提示 "确定要删除 xxx 吗？(y/N)"
  输入 y 或 Y 确认删除，输入其他则取消操作`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			bucket, object, err := parseBucketPath(args[0])
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			if err := requirePath(object, "del"); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			deleter := operations.NewDeleter(client)

			if recursive {
				deleter.DeleteDir(cmd.Context(), bucket, object, force, concurrent)
			} else {
				deleter.Delete(cmd.Context(), bucket, object, force)
			}
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "递归删除整个目录")
	cmd.Flags().IntVarP(&concurrent, "concurrent", "c", 0, "并发删除数量（仅与 -r 一起使用）")
	cmd.Flags().BoolVar(&force, "force", false, "强制删除，不进行确认提示")
	return cmd
}

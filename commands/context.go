package commands

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// ContextInfo 用于展示和操作的 context 摘要
type ContextInfo struct {
	Name      string
	Endpoint  string
	UseSSL    bool
	IsCurrent bool
}

// ContextOps 由 main 包注入的 context 操作回调
type ContextOps struct {
	ConfigPathFn   func() string
	ReadOnlyFn     func() bool
	ListFn         func() ([]ContextInfo, error)
	CurrentFn      func() (string, error)
	SetCurrentFn   func(name string) error
	ShowFn         func(name string) (accessKey, secretKey string, info ContextInfo, err error)
	UpsertFn       func(name, endpoint string, useSSL bool, accessKey, secretKey string) error
	RenameFn       func(oldName, newName string) error
	DeleteFn       func(name string) error
	ImportFromFileFn func(filePath string) (imported []string, err error)
}

func (o ContextOps) configPath() string {
	if o.ConfigPathFn == nil {
		return ""
	}
	return o.ConfigPathFn()
}

// CtxOps 注入的全局 context 操作
var CtxOps ContextOps

// SetExternalConfigPath 由 main 包注入：把 --config 参数应用到 context 操作
var SetExternalConfigPath func(path string)

// NewContextCmd 创建 context 子命令
func NewContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "context 管理（仿 kubectl config context）",
		Long: `管理多个 S3 服务端 context。每个 context 包含 endpoint/usessl/accesskey/secretkey，
accesskey/secretkey 加密存储（机器绑定）。

子命令:
  list              列出所有 context
  current [name]    显示当前 context，或设置默认
  use <name>        临时切换当前 context（仅本次进程）
  set-default <name> 设置默认 context（落盘）
  show [name]       解密显示 context 详情（默认显示当前）
  set <name>        交互式创建/更新 context
  rename <old> <new> 重命名 context
  delete <name>     删除 context（rm 为别名）
  import <file>     从明文 conf 文件导入 context 到默认配置`,

		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// 让 --config 参数对 context 子命令生效
			if configPath != "" && SetExternalConfigPath != nil {
				SetExternalConfigPath(configPath)
			}
			return nil
		},
		Run: func(cmd *cobra.Command, args []string) {
			// 默认行为：列出所有
			listContexts()
		},
	}

	cmd.AddCommand(
		newContextListCmd(),
		newContextCurrentCmd(),
		newContextUseCmd(),
		newContextSetDefaultCmd(),
		newContextShowCmd(),
		newContextSetCmd(),
		newContextRenameCmd(),
		newContextDeleteCmd(),
		newContextImportCmd(),
	)

	return cmd
}

// ============= list =============

func newContextListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "列出所有 context",
		Aliases: []string{"ls"},
		Run:     func(cmd *cobra.Command, args []string) { listContexts() },
	}
}

func listContexts() {
	infos, err := CtxOps.ListFn()
	if err != nil {
		exitErr(err)
	}
	if CtxOps.ReadOnlyFn != nil && CtxOps.ReadOnlyFn() {
		fmt.Printf("(只读模式：使用 --config=%s，明文 conf 不允许写入)\n\n", CtxOps.configPath())
	}
	if len(infos) == 0 {
		fmt.Println("未找到任何 context")
		return
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	for _, info := range infos {
		marker := "  "
		if info.IsCurrent {
			marker = "* "
		}
		cur := ""
		if info.IsCurrent {
			cur = " (current)"
		}
		fmt.Printf("%s%-20s%s  %s\n", marker, info.Name, cur, info.Endpoint)
	}
}

// ============= current =============

func newContextCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current [name]",
		Short: "显示当前 context，或设置为指定 context",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				name, err := CtxOps.CurrentFn()
				if err != nil {
					exitErr(err)
				}
				fmt.Println(name)
				return
			}
			if err := CtxOps.SetCurrentFn(args[0]); err != nil {
				exitErr(err)
			}
			fmt.Printf("已设置默认 context 为 %q\n", args[0])
		},
	}
}

// ============= use（临时） =============

func newContextUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "切换当前 context（仅本次进程，不落盘）",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			// CLI 模式下，use 与 set-default 等价（CLI 每次都是新进程，无所谓"临时"）
			// 但仍提供独立语义以与 set-default 区分。
			if err := CtxOps.SetCurrentFn(args[0]); err != nil {
				exitErr(err)
			}
			fmt.Printf("默认 context 已切换为 %q\n", args[0])
		},
	}
}

// ============= set-default =============

func newContextSetDefaultCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-default <name>",
		Short: "设置默认 context（落盘）",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := CtxOps.SetCurrentFn(args[0]); err != nil {
				exitErr(err)
			}
			fmt.Printf("已设置默认 context 为 %q\n", args[0])
		},
	}
}

// ============= show =============

func newContextShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "解密显示 context 详情",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				// 优先使用 --context
				name = contextName
			}
			ak, sk, info, err := CtxOps.ShowFn(name)
			if err != nil {
				exitErr(err)
			}
			fmt.Println("Context 信息:")
			fmt.Printf("  Name:      %s\n", info.Name)
			if info.IsCurrent {
				fmt.Println("  Current:   yes")
			}
			fmt.Printf("  Endpoint:  %s\n", info.Endpoint)
			fmt.Printf("  UseSSL:    %t\n", info.UseSSL)
			fmt.Printf("  AccessKey: %s\n", ak)
			fmt.Printf("  SecretKey: %s\n", sk)
		},
	}
}

// ============= set =============

func newContextSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name>",
		Short: "交互式创建或更新 context",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			setContext(args[0])
		},
	}
}

func setContext(name string) {
	fmt.Printf("设置 context %q:\n", name)
	endpoint := readInput("Endpoint: ")
	if endpoint == "" {
		fmt.Println("错误: Endpoint 不能为空")
		return
	}
	accesskey := readInput("AccessKey: ")
	secretkey := readInput("SecretKey: ")
	if accesskey == "" || secretkey == "" {
		fmt.Println("错误: AccessKey 和 SecretKey 不能为空")
		return
	}
	useSSLInput := readInput("UseSSL (true/false, 默认 true): ")

	useSSL := true
	if useSSLInput != "" {
		parsed, err := strconv.ParseBool(useSSLInput)
		if err != nil {
			fmt.Println("错误: UseSSL 必须是 true 或 false")
			return
		}
		useSSL = parsed
	}

	if err := CtxOps.UpsertFn(name, endpoint, useSSL, accesskey, secretkey); err != nil {
		exitErr(err)
	}
	fmt.Printf("Context %q 已保存到 %s（加密存储，绑定当前机器）\n", name, CtxOps.configPath())
}

// ============= rename =============

func newContextRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "重命名 context",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			if err := CtxOps.RenameFn(args[0], args[1]); err != nil {
				exitErr(err)
			}
			fmt.Printf("Context 已从 %q 重命名为 %q\n", args[0], args[1])
		},
	}
}

// ============= delete =============

func newContextDeleteCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "删除 context",
		Args:    cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if !force {
				fmt.Printf("确定要删除 context %q 吗？(y/N): ", args[0])
				ans, _ := stdinReader.ReadString('\n')
				ans = strings.TrimSpace(strings.ToLower(ans))
				if ans != "y" && ans != "yes" {
					fmt.Println("已取消")
					return
				}
			}
			if err := CtxOps.DeleteFn(args[0]); err != nil {
				exitErr(err)
			}
			fmt.Printf("Context %q 已删除\n", args[0])
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "强制删除，不进行确认")
	return cmd
}

// ============= import =============

func newContextImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <file>",
		Short: "从明文 conf 文件导入 context 到默认配置",
		Long: `从指定文件（明文 conf 格式）导入 context 到默认配置 ~/.config/s3m/s3m.conf。

合并策略:
  - 导入文件中的 context 与默认配置同名时，覆盖默认配置
  - 默认配置独有的 context 保留
  - current-context 以导入文件为准：文件有 current-context 则同步为默认配置的
    current-context；没有（或指向不存在的 context）则用文件中第一个 context
  - 导入文件本身不会被修改

支持的文件格式（多 context 扁平 key=value）:
  - ctx.<name>.endpoint / ctx.<name>.usessl
  - ctx.<name>.accesskey / ctx.<name>.secretkey（明文，优先于 auth）
  - ctx.<name>.auth（仅 enc:aes: 密文）
导入后 accesskey/secretkey 会自动加密为 auth 写入默认配置。`,

		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			imported, err := CtxOps.ImportFromFileFn(args[0])
			if err != nil {
				exitErr(err)
			}
			fmt.Printf("已从 %s 导入 %d 个 context: %s\n", args[0], len(imported), strings.Join(imported, ", "))
			if name, err := CtxOps.CurrentFn(); err == nil {
				fmt.Printf("current-context: %s\n", name)
			}
		},
	}
}

// ============= utils =============

// 共享的 stdin reader，避免 bufio 缓冲丢失
var stdinReader = bufio.NewReader(os.Stdin)

func readInput(prompt string) string {
	fmt.Print(prompt)
	input, _ := stdinReader.ReadString('\n')
	return strings.TrimSpace(input)
}

func exitErr(err error) {
	fmt.Println("错误:", err)
	os.Exit(1)
}

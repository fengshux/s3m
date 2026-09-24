package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"s3m/crypto"
)

// Config S3M 客户端配置（从 context 派生出的运行期配置）
type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// Context 一个 context 的配置
type Context struct {
	Endpoint  string
	UseSSL    bool
	AccessKey string
	SecretKey string
}

// ContextStore 配置文件内存模型
type ContextStore struct {
	Current  string
	Contexts map[string]Context
	ReadOnly bool // --config 显式指定时为 true：写操作禁止
}

// 配置文件中 context 字段的前缀，例如 "ctx.prod.endpoint"
const (
	contextKeyPrefix  = "ctx."
	currentContextKey = "current-context"

	// ctx.<name>.<field> 支持的字段
	ctxFieldEndpoint  = "endpoint"
	ctxFieldUseSSL    = "usessl"
	ctxFieldSSLAlias  = "ssl"
	ctxFieldAuth      = "auth"
	ctxFieldAccessKey = "accesskey"
	ctxFieldSecretKey = "secretkey"
)

// legacyBareKeys 已废弃的旧格式顶级键（无 ctx. 前缀），出现即报错
var legacyBareKeys = []string{"endpoint", "accesskey", "secretkey", "usessl", "ssl"}

// 配置文件搜索路径（按优先级排序）
var configPaths = []string{
	"./s3m.conf",
	"~/.config/s3m/s3m.conf",
	"/etc/s3m/s3m.conf",
}

// LoadConfig 从配置文件加载当前 context 的运行期 Config。
// 保留旧接口；推荐使用 LoadContextStore。
func LoadConfig(configPath string) (*Config, error) {
	cfg, _, err := LoadContextStore(configPath, "")
	return cfg, err
}

// ParseContextStore 公开的解析接口（供 import 等场景复用）。
// readOnly 仅影响返回 store 的 ReadOnly 标记，不改变解析规则；
// 解析规则统一为：auth 仅密文，accesskey/secretkey 明文，旧格式直接报错。
func ParseContextStore(path string, readOnly bool) (*ContextStore, error) {
	return parseContextStore(expandPath(path), readOnly)
}

// LoadContextStore 加载配置文件，按以下顺序解析请求的 context：
//  1. requestedName 不为空时，优先按 requestedName 查找
//  2. 否则使用 current-context
//  3. 都为空时报错
//
// 当 configPath 非空时，store 标记为 ReadOnly（外部指定 conf 不写盘）。
func LoadContextStore(configPath, requestedName string) (*Config, string, error) {
	paths := configPaths
	readOnly := false
	if configPath != "" {
		paths = []string{configPath}
		readOnly = true
	}

	for _, p := range paths {
		store, err := parseContextStore(expandPath(p), readOnly)
		if err == nil {
			return resolveContext(store, requestedName, p)
		}
		if !os.IsNotExist(err) {
			return nil, "", fmt.Errorf("读取配置文件 %s 失败: %w", p, err)
		}
	}

	return nil, "", fmt.Errorf("未找到配置文件，请使用 's3m context set <name>' 创建 context")
}

// resolveContext 从 store 中选择目标 context，构造运行期 Config
func resolveContext(store *ContextStore, requestedName, sourcePath string) (*Config, string, error) {
	if len(store.Contexts) == 0 {
		return nil, "", errors.New("配置文件中未找到任何 context")
	}

	name := requestedName
	if name == "" {
		name = store.Current
	}
	if name == "" {
		return nil, "", fmt.Errorf("未指定 context，也未设置 current-context。可用 context: %s", joinNames(store))
	}

	ctx, ok := store.Contexts[name]
	if !ok {
		return nil, "", fmt.Errorf("context %q 不存在。可用 context: %s", name, joinNames(store))
	}

	cfg := &Config{
		Endpoint:  ctx.Endpoint,
		UseSSL:    ctx.UseSSL,
		AccessKey: ctx.AccessKey,
		SecretKey: ctx.SecretKey,
	}
	if err := cfg.Validate(); err != nil {
		return nil, "", fmt.Errorf("context %q 配置无效: %w", name, err)
	}
	return cfg, name, nil
}

func joinNames(store *ContextStore) string {
	names := make([]string, 0, len(store.Contexts))
	for n := range store.Contexts {
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}

// parseContextStore 解析配置文件。
//
// 支持的字段（ctx.<name>.<field>）：
//   - endpoint   : endpoint 地址
//   - usessl/ssl : 是否使用 SSL（true/false）
//   - accesskey  : 明文 AccessKey（仅明文，优先于 auth）
//   - secretkey  : 明文 SecretKey（仅明文，优先于 auth）
//   - auth       : 仅支持 enc:aes: 密文（由程序写入/导入自动生成）
//
// 规则：
//   - 同一 context 同时出现 auth 与 accesskey/secretkey 时，优先 accesskey/secretkey
//   - accesskey 与 secretkey 必须成对出现
//   - auth 必须是 enc:aes: 密文，不接受明文 AK\x1fSK
//   - 旧格式顶级键（endpoint/accesskey/secretkey/usessl/ssl，无 ctx. 前缀）直接报错
func parseContextStore(path string, readOnly bool) (*ContextStore, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	store := &ContextStore{
		Contexts: map[string]Context{},
		ReadOnly: readOnly,
	}
	// 记录每个 context 的明文/密文凭证来源，用于解析结束后确定优先级
	plainAK := map[string]string{}
	plainSK := map[string]string{}
	authAK := map[string]string{}
	authSK := map[string]string{}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}

		// current-context 不带前缀
		if key == currentContextKey {
			store.Current = value
			continue
		}

		// 旧格式顶级键（无 ctx. 前缀）直接报错
		if !strings.HasPrefix(key, contextKeyPrefix) {
			if containsString(legacyBareKeys, strings.ToLower(key)) {
				return nil, fmt.Errorf(
					"检测到已废弃的旧配置格式（%s），请改用 %s<name>.endpoint / %s<name>.usessl / %s<name>.accesskey / %s<name>.secretkey",
					key, contextKeyPrefix, contextKeyPrefix, contextKeyPrefix, contextKeyPrefix)
			}
			continue
		}

		// 新格式：ctx.<name>.<field>
		rest := strings.TrimPrefix(key, contextKeyPrefix)
		dot := strings.Index(rest, ".")
		if dot <= 0 {
			continue
		}
		name := rest[:dot]
		field := strings.ToLower(rest[dot+1:])

		ctx := store.Contexts[name]
		switch field {
		case ctxFieldEndpoint:
			ctx.Endpoint = value
		case ctxFieldUseSSL, ctxFieldSSLAlias:
			parsed, perr := strconv.ParseBool(value)
			if perr != nil {
				return nil, fmt.Errorf("配置项 %s 值无效: %s（应为 true/false）", key, value)
			}
			ctx.UseSSL = parsed
		case ctxFieldAccessKey:
			plainAK[name] = value
		case ctxFieldSecretKey:
			plainSK[name] = value
		case ctxFieldAuth:
			ak, sk, derr := decryptAuth(value)
			if derr != nil {
				return nil, fmt.Errorf("context %q auth 字段无效: %w", name, derr)
			}
			authAK[name] = ak
			authSK[name] = sk
		default:
			// 未知字段忽略
		}
		store.Contexts[name] = ctx
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// 解析结束后确定每个 context 的凭证：
	// 明文 accesskey/secretkey 优先于 auth 密文；成对出现
	for name := range store.Contexts {
		ctx := store.Contexts[name]
		ak, hasAK := plainAK[name]
		sk, hasSK := plainSK[name]
		switch {
		case hasAK && hasSK:
			ctx.AccessKey = ak
			ctx.SecretKey = sk
		case hasAK || hasSK:
			return nil, fmt.Errorf("context %q 的 accesskey 和 secretkey 必须同时出现", name)
		case len(authAK[name]) > 0 || len(authSK[name]) > 0:
			ctx.AccessKey = authAK[name]
			ctx.SecretKey = authSK[name]
		default:
			return nil, fmt.Errorf("context %q 缺少 accesskey/secretkey 或 auth", name)
		}
		store.Contexts[name] = ctx
	}

	return store, nil
}

// decryptAuth 处理 ctx.<name>.auth 字段：
//   - 必须是 enc:aes: 密文
//   - 不接受明文 AK\x1fSK
func decryptAuth(value string) (ak, sk string, err error) {
	if !crypto.IsEncrypted(value) {
		return "", "", errors.New("auth 必须是 enc:aes: 密文；明文请改用 accesskey/secretkey 字段")
	}
	return crypto.DecryptCredentials(value)
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// SaveContextStore 把 store 写到文件（扁平 key=value 格式）
func SaveContextStore(path string, store *ContextStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	names := make([]string, 0, len(store.Contexts))
	for n := range store.Contexts {
		names = append(names, n)
	}
	// 稳定顺序：current 在前
	sortStrings(names)
	ordered := []string{}
	if store.Current != "" {
		ordered = append(ordered, store.Current)
	}
	for _, n := range names {
		if n != store.Current {
			ordered = append(ordered, n)
		}
	}

	var b strings.Builder
	b.WriteString("# S3M contexts\n")
	if store.Current != "" {
		fmt.Fprintf(&b, "%s=%s\n", currentContextKey, store.Current)
	}
	for _, name := range ordered {
		ctx, ok := store.Contexts[name]
		if !ok {
			continue
		}
		auth, err := crypto.EncryptCredentials(ctx.AccessKey, ctx.SecretKey)
		if err != nil {
			return fmt.Errorf("context %q 加密失败: %w", name, err)
		}
		fmt.Fprintf(&b, "\n[%s]\n", name)
		fmt.Fprintf(&b, "%s%s.endpoint=%s\n", contextKeyPrefix, name, ctx.Endpoint)
		fmt.Fprintf(&b, "%s%s.usessl=%t\n", contextKeyPrefix, name, ctx.UseSSL)
		fmt.Fprintf(&b, "%s%s.auth=%s%s\n", contextKeyPrefix, name, crypto.EncryptedPrefix, auth)
	}

	return os.WriteFile(path, []byte(b.String()), 0600)
}

// sortStrings 简单字符串排序，避免引入 sort 包
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// expandPath 展开 ~ 为用户主目录
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

// GetConfigPath 返回当前用户主目录下的配置路径
func GetConfigPath() string {
	home, _ := os.UserHomeDir()
	return home + "/.config/s3m/s3m.conf"
}

// Validate 验证配置是否完整
func (c *Config) Validate() error {
	if c.Endpoint == "" {
		return errors.New("缺少 endpoint")
	}
	if c.AccessKey == "" {
		return errors.New("缺少 accesskey")
	}
	if c.SecretKey == "" {
		return errors.New("缺少 secretkey")
	}
	return nil
}

package main

import (
	"errors"
	"fmt"

	"s3m/commands"
)

type contextOps struct {
	configPath string
	readOnly   bool
}

func (o *contextOps) ConfigPath() string {
	if o.configPath == "" {
		o.configPath = GetConfigPath()
	}
	return o.configPath
}

func (o *contextOps) ReadOnly() bool {
	return o.readOnly
}

func (o *contextOps) loadStore() (*ContextStore, error) {
	return parseContextStore(o.ConfigPath(), o.readOnly)
}

func (o *contextOps) checkWritable() error {
	if o.readOnly {
		return fmt.Errorf("配置文件 %s 是只读模式（明文 conf），不允许写入\n提示: 取消 --config 参数使用默认路径 %s 来管理加密 context",
			o.ConfigPath(), GetConfigPath())
	}
	return nil
}

func (o *contextOps) List() ([]commands.ContextInfo, error) {
	store, err := o.loadStore()
	if err != nil {
		return nil, err
	}
	out := make([]commands.ContextInfo, 0, len(store.Contexts))
	for name, ctx := range store.Contexts {
		out = append(out, commands.ContextInfo{
			Name:      name,
			Endpoint:  ctx.Endpoint,
			UseSSL:    ctx.UseSSL,
			IsCurrent: name == store.Current,
		})
	}
	return out, nil
}

func (o *contextOps) CurrentName() (string, error) {
	store, err := o.loadStore()
	if err != nil {
		return "", err
	}
	if store.Current == "" {
		return "", errors.New("未设置 current-context，请使用 's3m context set <name>' 创建")
	}
	return store.Current, nil
}

func (o *contextOps) SetCurrent(name string) error {
	if err := o.checkWritable(); err != nil {
		return err
	}
	store, err := o.loadStore()
	if err != nil {
		return err
	}
	if _, ok := store.Contexts[name]; !ok {
		return fmt.Errorf("context %q 不存在", name)
	}
	store.Current = name
	return SaveContextStore(o.ConfigPath(), store)
}

func (o *contextOps) Show(name string) (string, string, commands.ContextInfo, error) {
	store, err := o.loadStore()
	if err != nil {
		return "", "", commands.ContextInfo{}, err
	}
	if name == "" {
		name = store.Current
	}
	if name == "" {
		return "", "", commands.ContextInfo{}, errors.New("未指定 context，也未设置 current-context")
	}
	ctx, ok := store.Contexts[name]
	if !ok {
		return "", "", commands.ContextInfo{}, fmt.Errorf("context %q 不存在", name)
	}
	return ctx.AccessKey, ctx.SecretKey, commands.ContextInfo{
		Name:      name,
		Endpoint:  ctx.Endpoint,
		UseSSL:    ctx.UseSSL,
		IsCurrent: name == store.Current,
	}, nil
}

func (o *contextOps) Upsert(name, endpoint string, useSSL bool, accessKey, secretKey string) error {
	if err := o.checkWritable(); err != nil {
		return err
	}
	store, err := o.loadStore()
	if err != nil {
		store = &ContextStore{Contexts: map[string]Context{}}
	}
	store.Contexts[name] = Context{
		Endpoint:  endpoint,
		UseSSL:    useSSL,
		AccessKey: accessKey,
		SecretKey: secretKey,
	}
	if store.Current == "" {
		store.Current = name
	}
	return SaveContextStore(o.ConfigPath(), store)
}

func (o *contextOps) Rename(oldName, newName string) error {
	if err := o.checkWritable(); err != nil {
		return err
	}
	if oldName == newName {
		return nil
	}
	store, err := o.loadStore()
	if err != nil {
		return err
	}
	ctx, ok := store.Contexts[oldName]
	if !ok {
		return fmt.Errorf("context %q 不存在", oldName)
	}
	if _, dup := store.Contexts[newName]; dup {
		return fmt.Errorf("context %q 已存在", newName)
	}
	delete(store.Contexts, oldName)
	store.Contexts[newName] = ctx
	if store.Current == oldName {
		store.Current = newName
	}
	return SaveContextStore(o.ConfigPath(), store)
}

func (o *contextOps) Delete(name string) error {
	if err := o.checkWritable(); err != nil {
		return err
	}
	store, err := o.loadStore()
	if err != nil {
		return err
	}
	if _, ok := store.Contexts[name]; !ok {
		return fmt.Errorf("context %q 不存在", name)
	}
	delete(store.Contexts, name)
	if store.Current == name {
		store.Current = ""
	}
	return SaveContextStore(o.ConfigPath(), store)
}

// ImportFromFile 从 filePath 读取 context 并合并到默认 conf。
// 合并策略：
//   - 默认 conf 中同名 context 被覆盖；独有的 context 保留
//   - current-context 以导入文件为准：文件有 current-context 则同步为默认 conf 的
//     current-context；没有（或指向不存在的 context）则用文件中第一个 context
//   - 返回被新增或覆盖的 context 名称列表（按导入文件中出现顺序）
func (o *contextOps) ImportFromFile(filePath string) ([]string, error) {
	srcStore, err := ParseContextStore(filePath, true)
	if err != nil {
		return nil, fmt.Errorf("解析导入文件 %s 失败: %w", filePath, err)
	}
	if len(srcStore.Contexts) == 0 {
		return nil, fmt.Errorf("导入文件 %s 中未找到任何 context", filePath)
	}

	// 加载默认 conf（直接 ParseContextStore，readOnly=false，正常解析 + 触发迁移）
	defaultPath := GetConfigPath()
	defaultStore, err := ParseContextStore(defaultPath, false)
	if err != nil {
		// 默认 conf 不存在：创建新 store
		defaultStore = &ContextStore{Contexts: map[string]Context{}}
	}

	// 合并：srcStore 覆盖 defaultStore
	touched := make([]string, 0, len(srcStore.Contexts))
	for _, name := range srcStore.Order {
		ctx, ok := srcStore.Contexts[name]
		if !ok {
			continue
		}
		defaultStore.Contexts[name] = ctx
		touched = append(touched, name)
	}

	// current-context 以导入文件为准：
	// 文件有 current-context 且存在 → 同步；否则回退到文件中第一个 context
	if _, ok := srcStore.Contexts[srcStore.Current]; ok {
		defaultStore.Current = srcStore.Current
	} else {
		defaultStore.Current = srcStore.Order[0]
	}

	// 写入默认 conf
	if err := SaveContextStore(defaultPath, defaultStore); err != nil {
		return nil, fmt.Errorf("写入默认配置失败: %w", err)
	}

	return touched, nil
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"s3m/crypto"
)

func writeTempConf(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "s3m.conf")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("写入测试配置文件失败: %v", err)
	}
	return path
}

func TestParseContextStorePlaintextAKSK(t *testing.T) {
	path := writeTempConf(t, `
current-context=dev

[dev]
ctx.dev.endpoint=10.0.0.1:9000
ctx.dev.usessl=false
ctx.dev.accesskey=AKDEV
ctx.dev.secretkey=SKDEV
`)
	store, err := ParseContextStore(path, true)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	ctx, ok := store.Contexts["dev"]
	if !ok {
		t.Fatal("缺少 context dev")
	}
	if ctx.Endpoint != "10.0.0.1:9000" || ctx.UseSSL != false {
		t.Errorf("endpoint/usessl 解析错误: %+v", ctx)
	}
	if ctx.AccessKey != "AKDEV" || ctx.SecretKey != "SKDEV" {
		t.Errorf("accesskey/secretkey 解析错误: %+v", ctx)
	}
	if store.Current != "dev" {
		t.Errorf("current 解析错误: %q", store.Current)
	}
}

func TestParseContextStoreEncryptedAuth(t *testing.T) {
	enc, err := crypto.EncryptCredentials("AKENC", "SKENC")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	enc = crypto.EncryptedPrefix + enc
	path := writeTempConf(t, `
[prod]
ctx.prod.endpoint=s3.example.com
ctx.prod.usessl=true
ctx.prod.auth=`+enc+`
`)
	store, err := ParseContextStore(path, false)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	ctx, ok := store.Contexts["prod"]
	if !ok {
		t.Fatal("缺少 context prod")
	}
	if ctx.AccessKey != "AKENC" || ctx.SecretKey != "SKENC" {
		t.Errorf("auth 解密错误: %+v", ctx)
	}
}

func TestParseContextStorePlainAuthRejected(t *testing.T) {
	path := writeTempConf(t, `
[dev]
ctx.dev.endpoint=10.0.0.1:9000
ctx.dev.auth=AKDEV`+"\x1f"+`SKDEV
`)
	_, err := ParseContextStore(path, true)
	if err == nil {
		t.Fatal("明文 auth 应报错")
	}
	if !strings.Contains(err.Error(), "auth 必须是 enc:aes: 密文") {
		t.Errorf("错误信息不符合预期: %v", err)
	}
}

func TestParseContextStorePlainOverridesAuth(t *testing.T) {
	enc, err := crypto.EncryptCredentials("AKAUTH", "SKAUTH")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	enc = crypto.EncryptedPrefix + enc
	path := writeTempConf(t, `
[dev]
ctx.dev.endpoint=10.0.0.1:9000
ctx.dev.auth=`+enc+`
ctx.dev.accesskey=AKPLAIN
ctx.dev.secretkey=SKPLAIN
`)
	store, err := ParseContextStore(path, true)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	ctx := store.Contexts["dev"]
	if ctx.AccessKey != "AKPLAIN" || ctx.SecretKey != "SKPLAIN" {
		t.Errorf("明文应优先于 auth，实际: %+v", ctx)
	}
}

func TestParseContextStorePlainMissingOneRejected(t *testing.T) {
	cases := []string{
		"[dev]\nctx.dev.endpoint=e\nctx.dev.accesskey=AK\n",
		"[dev]\nctx.dev.endpoint=e\nctx.dev.secretkey=SK\n",
	}
	for i, content := range cases {
		path := writeTempConf(t, content)
		_, err := ParseContextStore(path, true)
		if err == nil {
			t.Fatalf("case %d: 只出现一半明文凭证应报错", i)
		}
		if !strings.Contains(err.Error(), "必须同时出现") {
			t.Errorf("case %d: 错误信息不符合预期: %v", i, err)
		}
	}
}

func TestParseContextStoreNoCredsRejected(t *testing.T) {
	path := writeTempConf(t, `
[dev]
ctx.dev.endpoint=10.0.0.1:9000
`)
	_, err := ParseContextStore(path, true)
	if err == nil {
		t.Fatal("无凭证的 context 应报错")
	}
	if !strings.Contains(err.Error(), "缺少 accesskey/secretkey 或 auth") {
		t.Errorf("错误信息不符合预期: %v", err)
	}
}

func TestParseContextStoreLegacyRejected(t *testing.T) {
	cases := []string{
		"endpoint=10.0.0.1:9000\nusessl=false\naccesskey=AK\nsecretkey=SK\n",
		"accesskey=AK\nsecretkey=SK\n",
		"endpoint=10.0.0.1:9000\n",
	}
	for i, content := range cases {
		path := writeTempConf(t, content)
		_, err := ParseContextStore(path, true)
		if err == nil {
			t.Fatalf("case %d: 旧格式应报错", i)
		}
		if !strings.Contains(err.Error(), "已废弃的旧配置格式") {
			t.Errorf("case %d: 错误信息不符合预期: %v", i, err)
		}
	}
}

func TestSaveContextStoreWritesEncryptedAuthOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s3m.conf")
	store := &ContextStore{
		Current: "dev",
		Contexts: map[string]Context{
			"dev": {
				Endpoint:  "10.0.0.1:9000",
				UseSSL:    false,
				AccessKey: "AKDEV",
				SecretKey: "SKDEV",
			},
		},
	}
	if err := SaveContextStore(path, store); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "ctx.dev.accesskey") || strings.Contains(content, "ctx.dev.secretkey") {
		t.Errorf("保存结果不应包含明文 accesskey/secretkey:\n%s", content)
	}
	if !strings.Contains(content, "ctx.dev.auth=enc:aes:") {
		t.Errorf("保存结果应包含加密 auth:\n%s", content)
	}
}

func TestImportFromFileEncryptsPlaintext(t *testing.T) {
	src := writeTempConf(t, `
[dev]
ctx.dev.endpoint=10.0.0.1:9000
ctx.dev.usessl=false
ctx.dev.accesskey=AKIMPORT
ctx.dev.secretkey=SKIMPORT
`)
	home := t.TempDir()
	t.Setenv("HOME", home)

	ops := &contextOps{}
	imported, err := ops.ImportFromFile(src)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if len(imported) != 1 || imported[0] != "dev" {
		t.Fatalf("导入结果不符合预期: %v", imported)
	}

	defaultPath := GetConfigPath()
	data, err := os.ReadFile(defaultPath)
	if err != nil {
		t.Fatalf("读取默认配置失败: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "ctx.dev.accesskey") || strings.Contains(content, "ctx.dev.secretkey") {
		t.Errorf("导入后不应保留明文 accesskey/secretkey:\n%s", content)
	}
	if !strings.Contains(content, "ctx.dev.auth=enc:aes:") {
		t.Errorf("导入后应写回加密 auth:\n%s", content)
	}

	store, err := ParseContextStore(defaultPath, false)
	if err != nil {
		t.Fatalf("回读默认配置失败: %v", err)
	}
	ctx := store.Contexts["dev"]
	if ctx.AccessKey != "AKIMPORT" || ctx.SecretKey != "SKIMPORT" {
		t.Errorf("导入后解密凭证不符合预期: %+v", ctx)
	}
}

func TestImportFromFileLegacyRejected(t *testing.T) {
	src := writeTempConf(t, "endpoint=10.0.0.1:9000\naccesskey=AK\nsecretkey=SK\n")
	home := t.TempDir()
	t.Setenv("HOME", home)

	ops := &contextOps{}
	_, err := ops.ImportFromFile(src)
	if err == nil {
		t.Fatal("导入旧格式文件应报错")
	}
	if !strings.Contains(err.Error(), "已废弃的旧配置格式") {
		t.Errorf("错误信息不符合预期: %v", err)
	}
}

func TestImportFromFileSyncsCurrentContext(t *testing.T) {
	src := writeTempConf(t, `
current-context=staging

[dev]
ctx.dev.endpoint=10.0.0.1:9000
ctx.dev.usessl=false
ctx.dev.accesskey=AKDEV
ctx.dev.secretkey=SKDEV

[staging]
ctx.staging.endpoint=s3.staging.com
ctx.staging.usessl=true
ctx.staging.accesskey=AKSTAGING
ctx.staging.secretkey=SKSTAGING
`)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// 默认 conf 已有 current-context=dev，导入文件 current-context=staging 应覆盖
	ops := &contextOps{}
	if err := ops.Upsert("dev", "old.example.com", false, "OLD", "OLD"); err != nil {
		t.Fatalf("准备默认 conf 失败: %v", err)
	}
	if _, err := ops.ImportFromFile(src); err != nil {
		t.Fatalf("导入失败: %v", err)
	}

	store, err := ParseContextStore(GetConfigPath(), false)
	if err != nil {
		t.Fatalf("回读默认配置失败: %v", err)
	}
	if store.Current != "staging" {
		t.Errorf("导入后 current 应为 staging，实际为 %q", store.Current)
	}
}

func TestImportFromFileFallsBackToFirstContext(t *testing.T) {
	src := writeTempConf(t, `
[zzz]
ctx.zzz.endpoint=10.0.0.1:9000
ctx.zzz.usessl=false
ctx.zzz.accesskey=AKZZZ
ctx.zzz.secretkey=SKZZZ

[aaa]
ctx.aaa.endpoint=s3.aaa.com
ctx.aaa.usessl=true
ctx.aaa.accesskey=AKAAA
ctx.aaa.secretkey=SKAAA
`)
	home := t.TempDir()
	t.Setenv("HOME", home)

	ops := &contextOps{}
	imported, err := ops.ImportFromFile(src)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if len(imported) != 2 || imported[0] != "zzz" || imported[1] != "aaa" {
		t.Errorf("导入顺序应按文件出现顺序: %v", imported)
	}

	store, err := ParseContextStore(GetConfigPath(), false)
	if err != nil {
		t.Fatalf("回读默认配置失败: %v", err)
	}
	if store.Current != "zzz" {
		t.Errorf("无 current-context 时应取文件中第一个 context，实际为 %q", store.Current)
	}
}

func TestImportFromFileInvalidCurrentFallsBack(t *testing.T) {
	src := writeTempConf(t, `
current-context=ghost

[dev]
ctx.dev.endpoint=10.0.0.1:9000
ctx.dev.usessl=false
ctx.dev.accesskey=AKDEV
ctx.dev.secretkey=SKDEV
`)
	home := t.TempDir()
	t.Setenv("HOME", home)

	ops := &contextOps{}
	if _, err := ops.ImportFromFile(src); err != nil {
		t.Fatalf("导入失败: %v", err)
	}

	store, err := ParseContextStore(GetConfigPath(), false)
	if err != nil {
		t.Fatalf("回读默认配置失败: %v", err)
	}
	if store.Current != "dev" {
		t.Errorf("current 指向不存在时应回退到文件中第一个 context，实际为 %q", store.Current)
	}
}

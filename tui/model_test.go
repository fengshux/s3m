package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"s3m/operations"
)

// newTestModel 构造测试模型并完成首帧尺寸初始化
func newTestModel() *Model {
	m := NewModel(nil)
	m.contextName = "test-ctx"
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return mm.(*Model)
}

// feedBuckets 模拟桶列表加载完成
func feedBuckets(m *Model, names ...string) *Model {
	buckets := make([]operations.BucketInfo, 0, len(names))
	for _, n := range names {
		buckets = append(buckets, operations.BucketInfo{Name: n, CreationDate: time.Now()})
	}
	mm, _ := m.Update(bucketsLoadedMsg{buckets: buckets})
	return mm.(*Model)
}

// feedObjects 模拟对象列表流式批次（first + done）
func feedObjects(m *Model, items []entry) *Model {
	mm, _ := m.Update(listStreamMsg{gen: m.loadGen, items: items, first: true})
	mm, _ = mm.Update(listStreamMsg{gen: m.loadGen, done: true})
	return mm.(*Model)
}

func testEntry(name string, isDir bool, size int64) entry {
	key := name
	if isDir {
		key += "/"
	}
	return entry{key: key, name: name, isDir: isDir, size: size, modified: time.Now()}
}

func press(m *Model, key string) *Model {
	var msg tea.KeyPressMsg
	switch {
	case key == "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case key == "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case key == "tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab}
	case key == " ":
		msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case strings.HasPrefix(key, "ctrl+"):
		msg = tea.KeyPressMsg{Code: rune(key[5]), Mod: tea.ModCtrl}
	default:
		msg = tea.KeyPressMsg{Code: rune(key[0]), Text: key}
	}
	mm, _ := m.Update(msg)
	return mm.(*Model)
}

// typeText 模拟向输入框逐字符输入
func typeText(m *Model, text string) *Model {
	for _, r := range text {
		mm, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mm.(*Model)
	}
	return m
}

func TestLayoutRenders(t *testing.T) {
	m := newTestModel()
	view := m.View().Content

	if !strings.Contains(view, "s3m") || !strings.Contains(view, "v0.1.0") {
		t.Errorf("标题栏缺少品牌/版本: %q", firstLine(view))
	}
	if !strings.Contains(view, "Profile:") || !strings.Contains(view, "test-ctx") {
		t.Error("标题栏缺少 Profile")
	}
	if !strings.Contains(view, "Buckets") {
		t.Error("缺少桶面板标题")
	}
}

func TestTooSmallTerminal(t *testing.T) {
	m := NewModel(nil)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	view := mm.(*Model).View().Content
	if !strings.Contains(view, "终端尺寸过小") {
		t.Error("小终端未提示")
	}
}

func TestBucketListAndEnter(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha", "beta")
	view := m.View().Content
	if !strings.Contains(view, "alpha") || !strings.Contains(view, "beta") {
		t.Error("桶列表未渲染")
	}
	if m.buckets.cursor != 0 {
		t.Errorf("光标初始应为 0，实际 %d", m.buckets.cursor)
	}

	// j 移动到第二个桶
	m = press(m, "j")
	if m.buckets.cursor != 1 {
		t.Errorf("j 后光标应为 1，实际 %d", m.buckets.cursor)
	}

	// Enter 进入桶 beta
	m = press(m, "enter")
	if m.currentBucket != "beta" {
		t.Errorf("应进入 beta，实际 %q", m.currentBucket)
	}
	if m.focus != FocusObjects {
		t.Error("进入桶后焦点应在对象面板")
	}

	// 模拟对象流式加载
	m = feedObjects(m, []entry{
		testEntry("docs", true, 0),
		testEntry("readme.md", false, 1024),
		testEntry("data.json", false, 2048),
	})
	if len(m.objects.items) != 4 { // back + 3
		t.Fatalf("对象应为 4 项（含返回行），实际 %d", len(m.objects.items))
	}
	if !m.objects.items[0].isBack {
		t.Error("首行应为返回行")
	}
	if !m.objects.items[1].isDir || m.objects.items[1].name != "docs" {
		t.Errorf("目录应排在文件前: %+v", m.objects.items[1])
	}
	view = m.View().Content
	if !strings.Contains(view, "readme.md") || !strings.Contains(view, "docs") {
		t.Error("对象未渲染")
	}
	if !strings.Contains(view, "Total: 3 objects") {
		t.Error("状态栏缺少对象统计")
	}
}

func TestPrefixNavigation(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha")
	m = press(m, "enter")
	m = feedObjects(m, []entry{testEntry("docs", true, 0), testEntry("a.txt", false, 5)})

	// 光标在 back 行，Enter 返回桶列表
	m = press(m, "enter")
	if m.inBucket() {
		t.Error("back 行 Enter 应返回桶列表")
	}

	// 重新进入，j 到目录行，Enter 进入子前缀
	m = feedBuckets(m, "alpha")
	m = press(m, "enter")
	m = feedObjects(m, []entry{testEntry("docs", true, 0), testEntry("a.txt", false, 5)})
	m = press(m, "j") // back -> docs
	m = press(m, "enter")
	if m.currentPrefix != "docs/" {
		t.Errorf("应进入 docs/，实际 %q", m.currentPrefix)
	}

	// h 返回上级
	m = press(m, "h")
	if m.currentPrefix != "" {
		t.Errorf("h 应返回桶根，实际 %q", m.currentPrefix)
	}

	// ~ 从深层回根
	m2 := newTestModel()
	m2 = feedBuckets(m2, "alpha")
	m2 = press(m2, "enter")
	m2.currentPrefix = "a/b/"
	m2 = press(m2, "~")
	if m2.currentPrefix != "" {
		t.Errorf("~ 应回桶根，实际 %q", m2.currentPrefix)
	}
}

func TestFilterFlow(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha")
	m = press(m, "enter")
	m = feedObjects(m, []entry{
		testEntry("docs", true, 0),
		testEntry("readme.md", false, 1024),
		testEntry("data.json", false, 2048),
		testEntry("report.md", false, 8),
	})

	m = press(m, "/")
	if m.inputMode != InputFilter {
		t.Fatal("未进入过滤模式")
	}
	// 输入 "md"（实时过滤）
	m = typeText(m, "md")

	if m.objects.visibleCount() != 3 { // back + readme.md + report.md
		t.Errorf("过滤后应剩 3 项（含返回行），实际 %d", m.objects.visibleCount())
	}

	// Esc 取消过滤
	m = press(m, "esc")
	if m.objects.visibleCount() != 5 {
		t.Errorf("取消过滤后应恢复 5 项（含返回行），实际 %d", m.objects.visibleCount())
	}
}

func TestBucketFilterFlow(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha", "beta", "gamma")

	// 桶面板焦点下按 / ，焦点不应跳到对象面板
	m = press(m, "/")
	if m.inputMode != InputFilter {
		t.Fatal("未进入过滤模式")
	}
	if m.focus != FocusBuckets {
		t.Fatalf("焦点不应跳到对象面板，实际 %v", m.focus)
	}

	// 输入 "al"（实时过滤，只剩 alpha）
	m = typeText(m, "al")
	if m.buckets.visibleCount() != 1 {
		t.Errorf("过滤后应剩 1 个桶，实际 %d", m.buckets.visibleCount())
	}
	if cur := m.buckets.current(); cur == nil || cur.key != "alpha" {
		t.Errorf("光标应停在 alpha 上，实际 %+v", cur)
	}
	view := m.View().Content
	if !strings.Contains(view, "alpha") || strings.Contains(view, "beta") {
		t.Error("桶过滤渲染不正确")
	}

	// Esc 取消过滤
	m = press(m, "esc")
	if m.inputMode != InputNone {
		t.Error("Esc 应退出过滤模式")
	}
	if m.buckets.visibleCount() != 3 {
		t.Errorf("取消过滤后应恢复 3 个桶，实际 %d", m.buckets.visibleCount())
	}
}

func TestMultiSelectAndBatchDeleteModal(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha")
	m = press(m, "enter")
	m = feedObjects(m, []entry{testEntry("a.txt", false, 1), testEntry("b.txt", false, 2)})

	// 无选择时 X 给提示
	m = press(m, "X")
	if m.activeModal != nil {
		t.Error("无选择时不应弹批量删除确认")
	}

	// Space 多选第一行（back），不应生效；选 b.txt
	m = press(m, "j") // a.txt
	m = press(m, " ")
	if m.objects.items[1].selected != true {
		t.Error("Space 未标记 a.txt")
	}
	m = press(m, "j")
	m = press(m, " ")
	if len(m.objects.selectedEntries()) != 2 {
		t.Errorf("应选中 2 项，实际 %d", len(m.objects.selectedEntries()))
	}

	m = press(m, "X")
	if m.activeModal == nil || m.activeModal.kind != ModalConfirm {
		t.Fatal("X 未弹出批量删除确认")
	}
	if !strings.Contains(m.View().Content, "共选中 2 个对象") {
		t.Error("批量删除确认未显示数量")
	}
	// N 取消
	m = press(m, "n")
	if m.activeModal != nil {
		t.Error("N 应取消确认弹窗")
	}
}

func TestDeleteConfirmModal(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha")
	m = press(m, "enter")
	m = feedObjects(m, []entry{testEntry("a.txt", false, 1)})
	m = press(m, "j") // a.txt

	m = press(m, "x")
	if m.activeModal == nil || m.activeModal.kind != ModalConfirm {
		t.Fatal("x 未弹出删除确认")
	}
	if !strings.Contains(m.View().Content, "此操作不可恢复") {
		t.Error("删除确认缺少警告")
	}
	m = press(m, "esc")
	if m.activeModal != nil {
		t.Error("Esc 应关闭删除确认")
	}
}

func TestHelpModal(t *testing.T) {
	m := newTestModel()
	m = press(m, "?")
	if m.activeModal == nil || m.activeModal.kind != ModalHelp {
		t.Fatal("? 未打开帮助")
	}
	view := m.View().Content
	for _, want := range []string{"Tab", "Ctrl+C", "列表导航"} {
		if !strings.Contains(view, want) {
			t.Errorf("帮助缺少 %q", want)
		}
	}
	// 翻页查看对象面板部分
	m = press(m, "ctrl+d")
	m = press(m, "ctrl+d")
	if !strings.Contains(m.View().Content, "Space") {
		t.Error("帮助滚动后缺少对象面板部分")
	}
	// 滚到底部查看后半部分
	m = press(m, "G")
	if !strings.Contains(m.View().Content, "预览模式") {
		t.Error("帮助滚动后缺少预览模式部分")
	}
	m = press(m, "esc")
	if m.activeModal != nil {
		t.Error("Esc 应关闭帮助")
	}
}

func TestQuitConfirm(t *testing.T) {
	m := newTestModel()
	m = press(m, "q")
	if m.activeModal == nil || m.activeModal.kind != ModalConfirm {
		t.Fatal("q 未弹出退出确认")
	}
	// N 默认取消
	m = press(m, "n")
	if m.activeModal != nil {
		t.Error("N 应取消退出")
	}
}

func TestPreviewFlow(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha")
	m = press(m, "enter")
	m = feedObjects(m, []entry{testEntry("readme.md", false, 100)})
	m = press(m, "j") // 选中 readme.md

	m = press(m, "v")
	if m.preview == nil {
		t.Fatal("v 未打开预览")
	}
	// 模拟预览内容加载完成
	mm, _ := m.Update(previewLoadedMsg{
		key:   "readme.md",
		lines: []string{"# hello", "world"},
		size:  100,
	})
	m = mm.(*Model)
	view := m.View().Content
	if !strings.Contains(view, "Preview:") || !strings.Contains(view, "readme.md") {
		t.Error("预览标题缺失")
	}
	if !strings.Contains(view, "# hello") {
		t.Error("预览内容缺失")
	}
	if !strings.Contains(view, "[Esc] Close") {
		t.Error("预览底部提示缺失")
	}

	// w 切换换行
	m = press(m, "w")
	if !m.preview.wrapped {
		t.Error("w 未切换换行")
	}
	// j 滚动不 panic
	m = press(m, "j")
	// Esc 关闭
	m = press(m, "esc")
	if m.preview != nil {
		t.Error("Esc 应关闭预览")
	}
}

func TestLargePreviewRejected(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha")
	m = press(m, "enter")
	m = feedObjects(m, []entry{testEntry("big.log", false, 11*1024*1024)})
	m = press(m, "j")

	m = press(m, "v")
	if m.activeModal == nil || m.activeModal.kind != ModalError {
		t.Fatal("超大文件应弹错误提示")
	}
	if !strings.Contains(m.View().Content, "s3m cat") {
		t.Error("超大文件应建议使用 s3m cat")
	}
}

func TestMediumPreviewConfirm(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha")
	m = press(m, "enter")
	m = feedObjects(m, []entry{testEntry("mid.log", false, 2*1024*1024)})
	m = press(m, "j")

	m = press(m, "v")
	if m.activeModal == nil || m.activeModal.kind != ModalConfirm {
		t.Fatal("1-10MB 文件应弹确认")
	}
	m = press(m, "y")
	if m.preview == nil {
		t.Error("确认后应开始预览")
	}
}

func TestMetaModal(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha")
	m = press(m, "enter")
	m = feedObjects(m, []entry{testEntry("a.txt", false, 1)})
	m = press(m, "j")

	m = press(m, "i")
	// 模拟 Stat 完成
	mm, _ := m.Update(objectInfoLoadedMsg{info: &operations.ObjectInfo{
		Key: "a.txt", Size: 100, ContentType: "text/plain",
		ETag: "abc", StorageClass: "STANDARD",
	}})
	m = mm.(*Model)
	if m.activeModal == nil || m.activeModal.kind != ModalMeta {
		t.Fatal("i 未弹出 Meta")
	}
	view := m.View().Content
	for _, want := range []string{"a.txt", "text/plain", "STANDARD", "ETag"} {
		if !strings.Contains(view, want) {
			t.Errorf("Meta 弹窗缺少 %q", want)
		}
	}
	m = press(m, "esc")
	if m.activeModal != nil {
		t.Error("Esc 应关闭 Meta")
	}
}

func TestCommandMode(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha", "beta")

	m = press(m, ":")
	if m.inputMode != InputCommand {
		t.Fatal("未进入命令模式")
	}

	// 输入 cd beta
	m = typeText(m, "cd beta")
	m = press(m, "enter")
	if m.currentBucket != "beta" {
		t.Errorf("cd beta 应切换桶，实际 %q", m.currentBucket)
	}

	// cd .. 返回
	m = press(m, ":")
	m = typeText(m, "cd ..")
	m = press(m, "enter")
	if m.inBucket() {
		t.Error("cd .. 应返回桶列表")
	}

	// 未知命令
	m = press(m, ":")
	m = typeText(m, "foobar")
	m = press(m, "enter")
	if m.statusKind != statusErr {
		t.Error("未知命令应报错")
	}
}

func TestHistoryModal(t *testing.T) {
	m := newTestModel()
	m.AddHistory("get", "bucket/a.txt", "success")
	m.activeModal = newHistoryModal(m.history)
	view := m.View().Content
	if !strings.Contains(view, "get") || !strings.Contains(view, "bucket/a.txt") {
		t.Error("历史弹窗内容缺失")
	}
}

func TestParentPrefix(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"a/":     "",
		"a/b/":   "a/",
		"a/b/c/": "a/b/",
	}
	for in, want := range cases {
		if got := parentPrefix(in); got != want {
			t.Errorf("parentPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatSize(t *testing.T) {
	cases := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		4308:       "4.2 KiB",
		1153433:    "1.1 MiB",
		1288490188: "1.2 GiB",
	}
	for in, want := range cases {
		if got := formatSize(in); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestIsTextEntry(t *testing.T) {
	if !isTextEntry(entry{name: "readme.md"}) {
		t.Error("md 应为文本")
	}
	if isTextEntry(entry{name: "photo.png"}) {
		t.Error("png 不应为文本")
	}
	if isTextEntry(entry{name: "docs", isDir: true}) {
		t.Error("目录不应判文本")
	}
}

func TestOverlayAndShorten(t *testing.T) {
	base := strings.Repeat("x", 40) + "\n" + strings.Repeat("y", 40)
	box := "hello"
	out := overlayCenter(base, box, 40)
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], "hello") {
		t.Error("覆盖层未生效")
	}
	if !strings.Contains(lines[0], "x") {
		t.Error("覆盖层应保留两侧内容")
	}

	if s := shorten("abcdefghij", 5); s != "abcd…" {
		t.Errorf("shorten = %q", s)
	}
}

func TestViewUsesConfiguredHeight(t *testing.T) {
	m := newTestModel()
	m = feedBuckets(m, "alpha")
	m = press(m, "enter")
	m = feedObjects(m, []entry{testEntry("readme.md", false, 1024)})

	lines := strings.Split(m.View().Content, "\n")
	if got := len(lines); got != m.height {
		t.Fatalf("视图行数 = %d, want %d", got, m.height)
	}
}

func TestTransferProgressRendering(t *testing.T) {
	origNow := nowFn
	defer func() { nowFn = origNow }()

	start := time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)
	nowFn = func() time.Time { return start }

	m := newTestModel()
	m = feedBuckets(m, "alpha")
	m = press(m, "enter")
	m = feedObjects(m, []entry{testEntry("readme.md", false, 1024)})

	mm, _ := m.Update(transferProgressMsg{op: "get", object: "alpha/readme.md", doneBytes: 512, totalBytes: 1024})
	m = mm.(*Model)
	nowFn = func() time.Time { return start.Add(2 * time.Second) }

	view := m.View().Content
	for _, want := range []string{"50%", "512 B / 1.0 KiB", "256 B/s", "ETA 2s"} {
		if !strings.Contains(view, want) {
			t.Fatalf("进度视图缺少 %q\n%s", want, view)
		}
	}

	mm, _ = m.Update(operationCompleteMsg{op: "get", object: "alpha/readme.md"})
	m = mm.(*Model)
	view = m.View().Content
	if strings.Contains(view, "50%") {
		t.Fatal("完成后仍显示旧进度")
	}
}

// firstLine 取首行（错误信息用）
func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

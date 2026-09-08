package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"s3m/operations"
)

// newHelpModal 帮助弹窗（spec 第三节键位表）
func newHelpModal() *modal {
	body := []string{
		"  全局",
		"    Tab          切换面板焦点（桶列表 ↔ 对象列表）",
		"    q            退出程序（确认弹窗）",
		"    Ctrl+C       强制退出",
		"    ?            打开帮助",
		"    :            命令模式",
		"",
		"  列表导航",
		"    j / ↓   k / ↑    上下移动",
		"    g   G           跳到顶部 / 底部",
		"    Ctrl+D / Ctrl+U 翻半页",
		"    /               输入过滤关键词（过滤当前面板）",
		"    n / N           下一个 / 上一个匹配",
		"",
		"  桶面板",
		"    Enter / l / →   进入桶",
		"    r               刷新桶列表",
		"",
		"  对象面板",
		"    Enter / l / →   进入目录 / 预览文本文件",
		"    h / ←           返回上一级前缀",
		"    ~               回到桶根目录",
		"    i               查看对象 Meta 信息",
		"    v               预览文本文件",
		"    u / U           上传文件 / 上传整个目录",
		"    d / D           下载对象 / 递归下载目录",
		"    x               删除选中对象（二次确认）",
		"    X               批量删除（多选模式）",
		"    Space           多选切换",
		"    r               刷新当前列表",
		"    c               复制对象 Key 到剪贴板",
		"",
		"  预览模式",
		"    Esc / q         关闭预览",
		"    j / k           上下滚动，Ctrl+D / Ctrl+U 翻页",
		"    w               切换自动换行",
		"",
		"  命令模式（:）",
		"    cd <path>       切换目录，cd .. 返回上级",
		"    ls              刷新列表",
		"    get <obj>       下载对象",
		"    put <file>      上传文件",
		"    sign <obj>      生成签名 URL",
		"    use <ctx>       切换 context",
		"    set-default <ctx> 切换并落盘默认 context",
		"    history         查看操作历史",
		"    exit / q        退出",
	}
	return &modal{kind: ModalHelp, title: "帮助 - 快捷键", body: body}
}

// newHistoryModal 历史弹窗
func newHistoryModal(history []HistoryEntry) *modal {
	body := make([]string, 0, len(history))
	if len(history) == 0 {
		body = append(body, "暂无历史记录")
	}
	for _, h := range history {
		icon := "✓"
		if h.Result != "success" {
			icon = "✗"
		}
		body = append(body, sprintf("%s %s %s %s: %s",
			h.Timestamp.Format("15:04:05"), icon, h.Operation,
			shorten(h.Object, 30), shorten(h.Result, 30)))
	}
	return &modal{kind: ModalHistory, title: sprintf("操作历史（最近 %d 条）", len(history)), body: body}
}

// newObjectMeta 从查询结果构建 Meta 载荷
func newObjectMeta(info *operations.ObjectInfo) *objectMeta {
	meta := &objectMeta{
		Key:          info.Key,
		Size:         info.Size,
		ContentType:  info.ContentType,
		LastModified: info.LastModified,
		ETag:         info.ETag,
		StorageClass: info.StorageClass,
	}
	for k, vs := range info.Metadata {
		for _, v := range vs {
			meta.Headers = append(meta.Headers, [2]string{k, v})
		}
	}
	return meta
}

// modalBodyRows 弹窗正文可滚动行数
func (m *Model) modalBodyRows() int {
	rows := m.height/2 - 4
	if rows < 5 {
		rows = 5
	}
	return rows
}

// renderModal 渲染弹窗
func (m Model) renderModal(mo *modal) string {
	var title string
	switch mo.kind {
	case ModalError:
		title = modalTitleErrStyle.Render("✗ " + mo.title)
	case ModalConfirm, ModalUploadConfirm:
		title = modalTitleWarnStyle.Render("⚠ " + mo.title)
	default:
		title = modalTitleStyle.Render(mo.title)
	}

	var body []string
	switch mo.kind {
	case ModalMeta:
		body = m.renderMetaBody(mo.meta)
	case ModalHelp, ModalHistory:
		body = m.renderScrollableBody(mo)
	default:
		body = m.renderStaticBody(mo)
	}

	width := m.modalWidth()
	lines := append([]string{title, modalDimStyle.Render(strings.Repeat("─", width-6))}, body...)
	lines = append(lines, m.renderModalFooter(mo))

	box := modalBoxStyle.Render(strings.Join(lines, "\n"))
	if ansi.StringWidth(box) > m.width-2 {
		box = modalBoxStyle.MaxWidth(m.width - 2).Render(strings.Join(lines, "\n"))
	}
	return box
}

// modalWidth 弹窗内容宽度
func (m *Model) modalWidth() int {
	w := m.width * 70 / 100
	if w > 78 {
		w = 78
	}
	if w < 50 {
		w = 50
	}
	return w
}

// renderStaticBody 静态正文（确认/错误）
func (m Model) renderStaticBody(mo *modal) []string {
	w := m.modalWidth() - 6
	var out []string
	for _, line := range mo.body {
		out = append(out, truncateWidth(line, w))
	}
	return out
}

// renderScrollableBody 可滚动正文（帮助/历史）
func (m Model) renderScrollableBody(mo *modal) []string {
	w := m.modalWidth() - 6
	rows := m.modalBodyRows()

	var out []string
	for i := mo.offset; i < mo.offset+rows && i < len(mo.body); i++ {
		out = append(out, truncateWidth(mo.body[i], w))
	}
	for len(out) < rows {
		out = append(out, "")
	}

	// 滚动指示
	if len(mo.body) > rows {
		pos := sprintf(" %d/%d ", mo.offset+1, len(mo.body))
		if mo.offset+rows >= len(mo.body) {
			pos = sprintf(" %d/%d ", len(mo.body), len(mo.body))
		}
		out = append(out, modalDimStyle.Render("  (j/k 滚动 "+pos+")"))
	}
	return out
}

// renderMetaBody Meta 信息正文
func (m Model) renderMetaBody(meta *objectMeta) []string {
	if meta == nil {
		return []string{"无信息"}
	}
	w := m.modalWidth() - 6

	row := func(label, value string) string {
		return modalLabelStyle.Render(padRight(label, 14)) + modalValueStyle.Render(truncateWidth(value, w-15))
	}

	lines := []string{
		row("Key", meta.Key),
		row("Size", formatSize(meta.Size)+" ("+itoa(int(meta.Size))+" B)"),
		row("Content-Type", ifEmpty(meta.ContentType, "-")),
		row("Last Modified", meta.LastModified.Format("2006-01-02 15:04:05")),
		row("ETag", ifEmpty(meta.ETag, "-")),
		row("Storage Class", ifEmpty(meta.StorageClass, "-")),
	}

	if len(meta.Headers) > 0 {
		lines = append(lines, "", modalDimStyle.Render("Headers:"))
		for i, h := range meta.Headers {
			if i >= 8 {
				lines = append(lines, modalDimStyle.Render(sprintf("  … 其余 %d 项", len(meta.Headers)-8)))
				break
			}
			lines = append(lines, modalDimStyle.Render("  "+truncateWidth(h[0]+": "+h[1], w-2)))
		}
	}
	return lines
}

// renderModalFooter 弹窗底部按键提示
func (m Model) renderModalFooter(mo *modal) string {
	switch mo.kind {
	case ModalHelp, ModalHistory:
		return modalDimStyle.Render("[j/k] 滚动  [Esc] 关闭")
	case ModalMeta:
		return modalDimStyle.Render("[Esc] 关闭")
	case ModalError:
		return modalDimStyle.Render("按任意键关闭")
	default:
		return modalYesStyle.Render("[Y] 确认") + "   " + modalDimStyle.Render("[N] 取消 (默认)")
	}
}

// pickerModalHeight 本地文件选择器弹窗内容高度
func (m Model) pickerModalHeight() int {
	h := m.height * 60 / 100
	if h > 20 {
		h = 20
	}
	if h < 5 {
		h = 5
	}
	return h
}

// renderPickerModal 本地文件选择器弹窗（上传）
func (m Model) renderPickerModal() string {
	title := "选择文件上传"
	if m.pickerDir {
		title = "选择目录上传"
	}
	target := "目标: " + m.currentBucket + "/" + m.currentPrefix

	h := m.pickerModalHeight()
	m.picker.SetHeight(h)

	header := modalTitleStyle.Render(title) + "  " + modalDimStyle.Render(shorten(target, 50))
	body := m.picker.View()

	footerKeys := "[Enter] 选择  [j/k] 移动  [l/→] 进入目录  [h/←] 返回上级  [Esc] 取消"
	if m.pickerDir {
		footerKeys = "[y] 选中当前目录  [Enter/l/→] 进入  [j/k] 移动  [h/←] 返回上级  [Esc] 取消"
	}
	footer := modalDimStyle.Render(footerKeys)

	return modalBoxStyle.Render(strings.Join([]string{
		header,
		modalDimStyle.Render(strings.Repeat("─", 46)),
		body,
		footer,
	}, "\n"))
}

package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// View 实现 tea.Model
func (m *Model) View() tea.View {
	if m.width < minTermWidth || m.height < minTermHeight {
		return tea.NewView(m.renderTooSmall())
	}

	var b strings.Builder
	b.WriteString(m.renderTitleBar())
	b.WriteString("\n")
	b.WriteString(m.renderMainArea())
	b.WriteString("\n")
	b.WriteString(m.renderActionLine())
	b.WriteString("\n")
	b.WriteString(m.renderStatusBar())

	out := b.String()
	if m.pickerActive {
		out = overlayCenter(out, m.renderPickerModal(), m.width)
	}
	if m.activeModal != nil {
		out = overlayCenter(out, m.renderModal(m.activeModal), m.width)
	}

	v := tea.NewView(out)
	v.AltScreen = true
	return v
}

// renderTooSmall 终端过小提示
func (m Model) renderTooSmall() string {
	return smallTermStyle.Render(
		"终端尺寸过小（当前 " + itoa(m.width) + "x" + itoa(m.height) + "）\n\n" +
			"s3m TUI 至少需要 " + itoa(minTermWidth) + "x" + itoa(minTermHeight) + "\n" +
			"请调大窗口后重试")
}

// renderTitleBar 标题栏：版本 / Profile / 端点 / 帮助
func (m Model) renderTitleBar() string {
	ctxLabel := m.contextName
	if ctxLabel == "" {
		ctxLabel = "?"
	}
	if m.readOnly {
		ctxLabel += " (readonly)"
	}

	left := titleBrandStyle.Render("s3m") + " " + titleItemStyle.Render(version)
	profile := titleItemStyle.Render("Profile: ") + titleItemStyle.Foreground(colCyan).Render(ctxLabel)
	endpoint := titleItemStyle.Render("Endpoint: ") + titleItemStyle.Render(m.endpointHost())
	help := titleHelpStyle.Render("? Help")

	gap := " " + separatorStyle.Render("│") + " "
	content := strings.Join([]string{left, profile, endpoint, help}, gap)
	content = " " + ansi.Truncate(content, maxInt(m.width-2, 0), "")
	padding := m.width - ansi.StringWidth(content)
	if padding > 0 {
		content += strings.Repeat(" ", padding)
	}
	return titleBarStyle.Render(content)
}

// endpointHost 当前端点主机名
func (m Model) endpointHost() string {
	if m.client == nil {
		return "-"
	}
	if u := m.client.EndpointURL(); u != nil {
		return u.Host
	}
	return "-"
}

// renderMainArea 主区域（含上下分隔线）：双面板或 预览分屏
func (m Model) renderMainArea() string {
	sep := separatorStyle.Render(rowSep(m.width))

	if m.preview != nil {
		return sep + "\n" + m.renderPreviewSplit() + "\n" + sep
	}
	return sep + "\n" + m.renderPanes() + "\n" + sep
}

// renderPanes 左右双面板：桶列表 | 对象列表（面板各自填满固定高度）
func (m Model) renderPanes() string {
	mainH := mainAreaHeight(m.height)
	leftW := leftPaneWidth(m.width)
	rightW := m.width - leftW - 1

	leftLines := m.renderBucketPane(leftW, mainH-1)
	rightLines := m.renderObjectPane(rightW, mainH)

	h := maxInt(len(leftLines), len(rightLines))
	var rows []string
	for i := 0; i < h; i++ {
		l, r := "", ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		rows = append(rows, padRight(l, leftW)+separatorStyle.Render("│")+padRight(r, rightW))
	}
	return strings.Join(rows, "\n")
}

// renderBucketPane 左侧桶面板（返回固定行数：标题 + 数据行 + 空白填充）
func (m Model) renderBucketPane(width, lines int) []string {
	out := []string{m.renderBucketHeader(width)}

	var body []string
	items := m.buckets.items
	switch {
	case len(items) == 0 && m.loading:
		body = append(body, m.focusedHintLine(width, "加载桶列表中…", FocusBuckets))
	case len(items) == 0:
		body = append(body, m.focusedHintLine(width, "无桶（按 r 刷新）", FocusBuckets))
	default:
		vis := m.buckets.visible()
		if len(vis) == 0 {
			body = append(body, m.focusedHintLine(width, "无匹配桶（Esc 取消过滤）", FocusBuckets))
		}
		for i := m.buckets.offset; i < m.buckets.offset+lines && i < len(vis); i++ {
			body = append(body, m.renderBucketRow(items[vis[i]], i == m.buckets.cursor, width))
		}
	}
	return appendFixedLines(out, body, lines)
}

// appendFixedLines 将 body 追加到 out 并填充空行至固定行数
func appendFixedLines(out, body []string, lines int) []string {
	for _, l := range body {
		if len(out) >= lines+1 {
			break
		}
		out = append(out, l)
	}
	for len(out) < lines+1 {
		out = append(out, "")
	}
	return out
}

// renderBucketHeader 桶面板标题行
func (m Model) renderBucketHeader(width int) string {
	title := paneHeaderStyle.Render("Buckets") + " " +
		paneHeaderDimStyle.Render("("+itoa(len(m.buckets.items))+")")
	if m.buckets.filter != "" {
		title += " " + filterStyle.Render("Filter: "+m.buckets.filter)
	}
	if m.focus == FocusBuckets {
		title = paneFocusMarkStyle.Render("▶ ") + title
	} else {
		title = "  " + title
	}
	return truncateWidth(title, width)
}

// renderBucketRow 桶行：▶ 光标 / ◎ 当前桶 / ○ 其他
func (m Model) renderBucketRow(e entry, cursor bool, width int) string {
	var mark, name string
	switch {
	case cursor:
		mark = paneFocusMarkStyle.Render("▶")
		name = dirStyle.Render(shorten(e.name, width-4))
	case e.key == m.currentBucket && m.inBucket():
		mark = activeStyle.Render("◎")
		name = dirStyle.Render(shorten(e.name, width-4))
	default:
		mark = paneHeaderDimStyle.Render("○")
		name = fileStyle.Render(shorten(e.name, width-4))
	}

	line := " " + mark + " " + name
	if cursor {
		return rowSelectedStyle.Width(width).Render(ansi.Truncate(line, width, ""))
	}
	return padRight(line, width)
}

// renderObjectPane 右侧对象面板（返回固定行数：标题/面包屑 + 数据行 + 页脚）
func (m Model) renderObjectPane(width, height int) []string {
	out := []string{m.renderObjectHeader(width)}

	rows := height - 2 // 减去标题与页脚
	if rows < 1 {
		rows = 1
	}

	var body []string
	if !m.inBucket() {
		body = append(body, m.focusedHintLine(width, "在左侧选择桶后按 Enter 浏览对象", FocusObjects))
	} else {
		vis := m.objects.visible()
		for i := m.objects.offset; i < m.objects.offset+rows && i < len(vis); i++ {
			body = append(body, m.renderObjectRow(m.objects.items[vis[i]], i == m.objects.cursor, width))
		}
		if len(vis) == 0 && !m.loading {
			body = append(body, m.focusedHintLine(width, "（空目录）", FocusObjects))
		}
	}
	out = appendFixedLines(out, body, height-2)
	return append(out, m.renderObjectFooter(width, rows))
}

// renderObjectHeader 对象面板标题（面包屑：桶 / 前缀）
func (m Model) renderObjectHeader(width int) string {
	var crumb string
	if m.inBucket() {
		crumb = paneHeaderStyle.Render("Objects: ") +
			crumbBucketStyle.Render(m.currentBucket) + " " +
			crumbPrefixStyle.Render(m.breadcrumbPrefix())
	} else {
		crumb = paneHeaderStyle.Render("Objects")
	}
	if m.focus == FocusObjects {
		crumb = paneFocusMarkStyle.Render("▶ ") + crumb
	} else {
		crumb = "  " + crumb
	}
	return truncateWidth(crumb, width)
}

// breadcrumbPrefix 面包屑前缀串（目录层级以 ► 分隔）
func (m Model) breadcrumbPrefix() string {
	parts := strings.Split(strings.TrimSuffix(m.currentPrefix, "/"), "/")
	var valid []string
	for _, p := range parts {
		if p != "" {
			valid = append(valid, p)
		}
	}
	if len(valid) == 0 {
		return "/"
	}
	return "/ " + strings.Join(valid, " ► ") + " /"
}

// renderObjectRow 对象行：多选标记 图标 日期 大小 名称
func (m Model) renderObjectRow(e entry, cursor bool, width int) string {
	var b strings.Builder

	// 多选标记
	if e.selected {
		b.WriteString(markStyle.Render("✔"))
	} else {
		b.WriteString(" ")
	}
	b.WriteString(" ")

	// 图标
	if e.isBack {
		b.WriteString(backStyle.Render("← "))
		b.WriteString(backStyle.Render(".."))
		line := b.String()
		if cursor {
			return rowSelectedStyle.Width(width).Render(ansi.Truncate(line, width, ""))
		}
		return padRight(line, width)
	}

	if e.isDir {
		b.WriteString(dirStyle.Render("📂"))
	} else {
		b.WriteString(fileStyle.Render("📄"))
	}
	b.WriteString(" ")

	// 日期列（窄终端隐藏）
	if m.width >= smallTermCols {
		if e.modified.IsZero() {
			b.WriteString(faintText(padLeft("-", dateColWidth)))
		} else {
			b.WriteString(faintText(padLeft(e.modified.Format("2006-01-02 15:04"), dateColWidth)))
		}
		b.WriteString(" ")
	}

	// 大小列
	size := "0 B"
	if !e.isDir {
		size = formatSize(e.size)
	}
	b.WriteString(faintText(padLeft(size, sizeColWidth)))
	b.WriteString(" ")

	// 名称
	nameWidth := width - usedColumns(m.width >= smallTermCols)
	if e.isDir {
		b.WriteString(dirStyle.Render(shorten(e.name, nameWidth)))
	} else {
		b.WriteString(fileStyle.Render(shorten(e.name, nameWidth)))
	}

	line := b.String()
	if cursor {
		return rowSelectedStyle.Width(width).Render(ansi.Truncate(line, width, ""))
	}
	return padRight(line, width)
}

// 列宽常量
const (
	dateColWidth = 16
	sizeColWidth = 8
)

// usedColumns 名称列之前的固定宽度（sel 2 + icon 2 + [date 17] + size 9）
func usedColumns(showDate bool) int {
	w := 2 + 2 + sizeColWidth + 1
	if showDate {
		w += dateColWidth + 1
	}
	return w
}

// faintText 弱化文字
func faintText(s string) string {
	return paneHeaderDimStyle.Render(s)
}

// renderObjectFooter 对象面板页脚：位置指示 + 过滤词 + spinner
func (m Model) renderObjectFooter(width, rows int) string {
	var parts []string

	switch {
	case m.transfer != nil:
		parts = append(parts, m.renderTransferProgress(width))
	case m.loading:
		parts = append(parts, spinnerStyle.Render(m.spinner.View())+" 加载中…")
	default:
		vis := m.objects.visible()
		pos := "[ - / - ]"
		if len(vis) > 0 {
			pos = fmt.Sprintf("[ %d/%d ]", m.objects.cursor+1, len(vis))
		}
		parts = append(parts, footerStyle.Render(pos))
	}

	if m.objects.filter != "" {
		parts = append(parts, filterStyle.Render("Filter: "+m.objects.filter))
	} else if m.transfer == nil {
		parts = append(parts, footerStyle.Render("Filter: _"))
	}

	line := " " + strings.Join(parts, "  ")
	return truncateWidth(line, width)
}

func (m Model) renderTransferProgress(width int) string {
	if m.transfer == nil {
		return ""
	}
	progress := m.transfer
	if progress.totalBytes <= 0 {
		return spinnerStyle.Render(m.spinner.View()) + "  " + progress.label
	}
	percent := 0
	if progress.totalBytes > 0 {
		percent = int((progress.doneBytes * 100) / progress.totalBytes)
	}
	percent = clampInt(percent, 0, 100)
	barWidth := clampInt(width/5, 12, 24)
	filled := 0
	if progress.totalBytes > 0 {
		filled = int((progress.doneBytes * int64(barWidth)) / progress.totalBytes)
	}
	filled = clampInt(filled, 0, barWidth)
	bar := "[" + strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled) + "]"

	elapsed := nowFn().Sub(progress.startedAt).Seconds()
	rate := 0.0
	if elapsed > 0 {
		rate = float64(progress.doneBytes) / elapsed
	}
	eta := "-"
	if rate > 0 && progress.totalBytes > progress.doneBytes {
		eta = formatETA(float64(progress.totalBytes-progress.doneBytes) / rate)
	}

	parts := []string{
		spinnerStyle.Render(m.spinner.View()),
		progress.label,
		bar,
		fmt.Sprintf("%d%%", percent),
		fmt.Sprintf("%s / %s", formatSize(progress.doneBytes), formatSize(progress.totalBytes)),
		formatRate(rate),
		"ETA " + eta,
	}
	return strings.Join(parts, "  ")
}

// renderActionLine 操作栏 / 输入行（单行）
func (m Model) renderActionLine() string {
	if m.inputMode != InputNone {
		return m.renderInputLine()
	}
	return m.renderActionBar()
}

// renderInputLine 输入模式行（命令/过滤/路径共用）
func (m Model) renderInputLine() string {
	var mode string
	switch m.inputMode {
	case InputCommand:
		mode = inputModeStyle.Render(" 命令 ")
	case InputFilter:
		mode = inputModeStyle.Render(" 过滤 ")
	default:
		mode = inputModeStyle.Render(" 路径 ")
	}
	line := " " + mode + " " + inputTextStyle.Render(m.input.View())
	line = ansi.Truncate(line, maxInt(m.width-1, 0), "")
	return actionBarStyle.Render(padRight(line, m.width))
}

// renderActionBar 上下文相关快捷键提示
func (m Model) renderActionBar() string {
	var keys []string
	add := func(k, label string) {
		keys = append(keys, actionKeyStyle.Render("["+k+"]")+" "+label)
	}

	if m.focus == FocusBuckets || !m.inBucket() {
		add("Enter", "进入桶")
		add("r", "刷新")
		add("Tab", "对象面板")
		add(":", "命令")
		add("?", "帮助")
		add("q", "退出")
	} else {
		add("i", "Meta")
		add("v", "预览")
		add("u", "上传")
		add("U", "传目录")
		add("d", "下载")
		add("D", "下目录")
		add("x", "删除")
		add("Space", "多选")
		add("X", "批删")
		add("Tab", "桶面板")
	}

	line := strings.Join(keys, actionSepStyle.Render(" │ "))
	line = " " + ansi.Truncate(line, maxInt(m.width-1, 0), "")
	return actionBarStyle.Render(padRight(line, m.width))
}

// renderStatusBar 状态栏
func (m Model) renderStatusBar() string {
	var left string
	switch m.statusKind {
	case statusOk:
		left = statusOkStyle.Render(shorten(m.statusText, m.width-30))
	case statusErr:
		left = statusErrStyle.Render(shorten(m.statusText, m.width-30))
	default:
		if m.inBucket() {
			info := sprintf("Bucket: %s │ Prefix: %s │ Total: %d objects",
				m.currentBucket, ifEmpty(m.currentPrefix, "/"), len(m.objects.items)-1)
			left = statusText(shorten(info, m.width-30))
		} else {
			left = statusText("Buckets: " + itoa(len(m.buckets.items)))
		}
	}

	right := statusText("ctx: " + m.contextLabel() + " │ 历史 " + itoa(len(m.history)))
	gap := m.width - 2 - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		gap = 1
	}
	line := " " + left + strings.Repeat(" ", gap) + right
	return statusBarStyle.Render(padRight(line, m.width))
}

// statusText 状态栏普通文字
func statusText(s string) string { return s }

// ifEmpty 空串兜底
func ifEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// contextLabel 标题/状态栏 context 标签
func (m Model) contextLabel() string {
	if m.contextName == "" {
		return "?"
	}
	if m.readOnly {
		return m.contextName + " (readonly)"
	}
	return m.contextName
}

// focusedHintLine 居中提示行
func (m Model) focusedHintLine(width int, text string, focus FocusPane) string {
	line := strings.Repeat(" ", maxInt((width-displayWidth(text))/2, 0)) + paneHeaderDimStyle.Render(text)
	return padRight(line, width)
}

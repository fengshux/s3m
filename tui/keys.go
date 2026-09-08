package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// handleKeyPress 按当前界面状态分发按键
func (m *Model) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.inputMode != InputNone:
		return m.handleInputKey(msg)
	case m.pickerActive:
		return m.handlePickerKey(msg)
	case m.preview != nil:
		return m.handlePreviewKey(msg)
	case m.activeModal != nil:
		return m.handleModalKey(msg)
	default:
		return m.handleNormalKey(msg)
	}
}

// handleInputKey 输入行（命令 / 过滤 / 路径）
func (m *Model) handleInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		value := m.input.Value()
		mode := m.inputMode
		m.inputMode = InputNone
		m.input.Blur()
		m.input.SetValue("")
		return m.commitInput(mode, value)
	case "esc":
		if m.inputMode == InputFilter {
			m.focusedPane().setFilter("")
		}
		m.inputMode = InputNone
		m.input.Blur()
		m.input.SetValue("")
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.inputMode == InputFilter {
		m.focusedPane().setFilter(m.input.Value())
	}
	return m, cmd
}

// commitInput 提交输入内容
func (m *Model) commitInput(mode InputMode, value string) (tea.Model, tea.Cmd) {
	switch mode {
	case InputCommand:
		return m.executeCommand(value)
	case InputFilter:
		m.focusedPane().setFilter(value) // 过滤已实时生效，此处仅收尾
		return m, nil
	case InputPath:
		return m.confirmPathInput(value)
	}
	return m, nil
}

// handlePickerKey 文件选择器（上传）
func (m *Model) handlePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.pickerActive = false
		return m, nil
	}

	// 目录模式下 Enter 仅用于下钻，用 y 显式选中当前高亮目录
	if m.pickerDir && (msg.String() == "y" || msg.String() == "Y") {
		if path := m.picker.HighlightedPath(); path != "" && isLocalDir(path) {
			m.pickerActive = false
			return m.openUploadConfirm(path)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.picker, cmd = m.picker.Update(msg)
	if !m.pickerDir {
		if didSelect, path := m.picker.DidSelectFile(msg); didSelect {
			m.pickerActive = false
			return m.openUploadConfirm(path)
		}
	}
	return m, cmd
}

// openUploadConfirm 打开上传确认弹窗
func (m *Model) openUploadConfirm(localPath string) (tea.Model, tea.Cmd) {
	bucket := m.currentBucket
	prefix := m.currentPrefix
	targetKey := localUploadTarget(prefix, localPath)
	if m.pickerDir {
		targetKey = prefix + baseName(localPath) + "/"
	}

	m.activeModal = &modal{
		kind:  ModalUploadConfirm,
		title: "上传确认",
		body: []string{
			"本地: " + shorten(localPath, 44),
			"目标: " + bucket + "/" + shorten(targetKey, 44),
		},
		local: localPath,
		onYes: func(mm *Model) tea.Cmd {
			mm.closeModal()
			return mm.beginUpload(localPath, targetKey)
		},
	}
	return m, nil
}

// beginUpload 执行上传（文件或目录）
func (m *Model) beginUpload(localPath, targetKey string) tea.Cmd {
	op := "put"
	if m.pickerDir {
		op = "put-dir"
	} else {
		m.transferCh = make(chan tea.Msg, 64)
	}
	m.setBusy(op, baseName(localPath))
	m.clearStatus()
	if m.pickerDir {
		return uploadDirCmd(m.putter, m.currentBucket, targetKey, localPath)
	}
	return uploadCmd(m.putter, m.currentBucket, targetKey, localPath, m.transferCh)
}

// handlePreviewKey 预览模式按键（spec 第三节预览模式）
func (m *Model) handlePreviewKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.preview
	switch msg.String() {
	case "esc", "q":
		m.closePreview()
		m.resize(m.width, m.height)
		return m, nil
	case "j", "down":
		p.scroll(1, len(p.displayLines(p.cols)), p.rows)
	case "k", "up":
		p.scroll(-1, len(p.displayLines(p.cols)), p.rows)
	case "ctrl+d":
		p.scroll(p.rows/2, len(p.displayLines(p.cols)), p.rows)
	case "ctrl+u":
		p.scroll(-p.rows/2, len(p.displayLines(p.cols)), p.rows)
	case "g":
		p.offset = 0
	case "G":
		p.offset = 1 << 30
		p.clamp(len(p.displayLines(p.cols)), p.rows)
	case "w":
		p.toggleWrap()
	default:
		return m, nil
	}
	return m, nil
}

// handleModalKey 弹窗按键
func (m *Model) handleModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	mo := m.activeModal
	switch mo.kind {
	case ModalHelp, ModalHistory:
		return m.handleScrollableModal(msg)

	case ModalMeta:
		switch msg.String() {
		case "esc", "q", "i":
			m.closeModal()
		}
		return m, nil

	case ModalError:
		m.closeModal()
		return m, nil

	case ModalConfirm, ModalUploadConfirm:
		switch strings.ToLower(msg.String()) {
		case "y":
			if mo.onYes != nil {
				fn := mo.onYes
				m.closeModal()
				return m, fn(m)
			}
			m.closeModal()
		case "n", "esc", "q":
			m.closeModal()
		}
		return m, nil
	}
	return m, nil
}

// handleScrollableModal 可滚动弹窗（帮助 / 历史）
func (m *Model) handleScrollableModal(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	mo := m.activeModal
	total := len(mo.body)
	rows := m.modalBodyRows()
	switch msg.String() {
	case "esc", "q", "?":
		m.closeModal()
	case "j", "down":
		mo.offset++
	case "k", "up":
		mo.offset--
	case "g":
		mo.offset = 0
	case "G":
		mo.offset = total
	case "ctrl+d":
		mo.offset += rows / 2
	case "ctrl+u":
		mo.offset -= rows / 2
	default:
		return m, nil
	}
	if mo.offset > total-rows {
		mo.offset = total - rows
	}
	if mo.offset < 0 {
		mo.offset = 0
	}
	return m, nil
}

// handleNormalKey 常规模式按键（全局 + 焦点面板）
func (m *Model) handleNormalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.transfer != nil && blocksDuringTransfer(key) {
		m.setStatus(statusNone, "当前任务进行中，请等待完成")
		return m, nil
	}

	// 全局按键
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		return m.toggleFocus()
	case "?":
		m.activeModal = newHelpModal()
		return m, nil
	case ":":
		return m.enterInput(InputCommand, ": ", "命令: cd/ls/get/put/sign/use/history/exit", "")
	case "/":
		return m.enterInput(InputFilter, "/ ", "输入过滤关键词", "")
	case "q":
		return m.openQuitConfirm()
	}

	// 列表导航（Vim 风格，作用于焦点面板）
	if cmd, handled := m.handleNavigationKey(key); handled {
		return m, cmd
	}

	// 面板特有按键
	if m.focus == FocusBuckets {
		return m.handleBucketKey(key)
	}
	return m.handleObjectKey(key)
}

func blocksDuringTransfer(key string) bool {
	switch key {
	case "ctrl+c", "?", "q", "tab", "j", "k", "g", "G", "up", "down", "ctrl+d", "ctrl+u", "n", "N":
		return false
	default:
		return true
	}
}

// toggleFocus 切换面板焦点
func (m *Model) toggleFocus() (tea.Model, tea.Cmd) {
	if m.focus == FocusBuckets {
		m.focus = FocusObjects
	} else {
		m.focus = FocusBuckets
	}
	return m, nil
}

// handleNavigationKey 全局导航键，返回是否已处理
func (m *Model) handleNavigationKey(key string) (tea.Cmd, bool) {
	p := m.focusedPane()
	switch key {
	case "j", "down":
		p.moveCursor(1)
	case "k", "up":
		p.moveCursor(-1)
	case "g":
		p.top()
	case "G":
		p.bottom()
	case "ctrl+d":
		p.moveCursor(p.halfPage())
	case "ctrl+u":
		p.moveCursor(-p.halfPage())
	case "n":
		return m.jumpFilter(true), true
	case "N":
		return m.jumpFilter(false), true
	default:
		return nil, false
	}
	m.clearStatus()
	return nil, true
}

// jumpFilter 过滤匹配项跳转（作用于焦点面板）
func (m *Model) jumpFilter(next bool) tea.Cmd {
	if !m.focusedPane().jumpMatch(next) {
		m.setStatus(statusNone, "无过滤词，按 / 输入过滤后使用 n/N 跳转")
	}
	return nil
}

// focusedPane 当前焦点面板
func (m *Model) focusedPane() *pane {
	if m.focus == FocusBuckets {
		return &m.buckets
	}
	return &m.objects
}

// handleBucketKey 桶面板按键
func (m *Model) handleBucketKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter", "l", "right":
		if cur := m.buckets.current(); cur != nil {
			m.focus = FocusObjects
			return m, m.openBucket(cur.key)
		}
	case "r":
		return m, loadBucketsCmd(m.lister)
	}
	return m, nil
}

// handleObjectKey 对象面板按键
func (m *Model) handleObjectKey(key string) (tea.Model, tea.Cmd) {
	if !m.inBucket() {
		return m, nil
	}
	e := m.objects.current()

	switch key {
	case "enter", "l", "right":
		return m, m.enterEntry()
	case "h", "left", "backspace":
		return m, m.goUp()
	case "~":
		return m, m.goRoot()
	case "r":
		return m, m.refreshActive()
	case "i":
		if e != nil && !e.isBack && !e.isDir {
			return m, statObjectCmd(m.stater, m.currentBucket, e.key)
		}
		m.setStatus(statusNone, "请选中一个文件后按 i 查看 Meta")
	case "v":
		if e != nil && !e.isBack && !e.isDir {
			m.openPreview(*e)
			return m, nil
		}
		m.setStatus(statusNone, "请选中一个文本文件后按 v 预览")
	case "u":
		return m.openUploadPicker(false)
	case "U":
		return m.openUploadPicker(true)
	case "d":
		return m.openDownloadInput()
	case "D":
		return m.openDownloadDirInput()
	case "x":
		return m.openDeleteConfirm()
	case "X":
		return m.openBatchDeleteConfirm()
	case " ", "space":
		m.objects.toggleSelectCurrent()
	case "c":
		return m.copyCurrentKey()
	}
	return m, nil
}

// openQuitConfirm 退出确认（默认 N，spec 4.8 风格）
func (m *Model) openQuitConfirm() (tea.Model, tea.Cmd) {
	m.activeModal = &modal{
		kind:  ModalConfirm,
		title: "退出确认",
		body:  []string{"确定要退出 s3m 吗？"},
		onYes: func(mm *Model) tea.Cmd {
			return tea.Quit
		},
	}
	return m, nil
}

// openUploadPicker 打开本地文件选择器
func (m *Model) openUploadPicker(dir bool) (tea.Model, tea.Cmd) {
	m.pickerDir = dir
	m.picker.FileAllowed = !dir
	m.picker.DirAllowed = dir
	m.picker.SetHeight(m.pickerModalHeight())
	m.pickerActive = true
	return m, m.picker.Init()
}

// openDownloadInput 打开下载路径输入（单对象）
func (m *Model) openDownloadInput() (tea.Model, tea.Cmd) {
	e := m.objects.current()
	if e == nil || e.isBack || e.isDir {
		m.setStatus(statusNone, "请选中一个文件后按 d 下载（目录用 D）")
		return m, nil
	}
	m.pathInput = pathInput{purpose: purposeDownloadFile, target: *e}
	return m.enterInput(InputPath, "保存至: ", "本地保存路径", e.name)
}

// openDownloadDirInput 打开目录下载路径输入
func (m *Model) openDownloadDirInput() (tea.Model, tea.Cmd) {
	e := m.objects.current()
	if e == nil || e.isBack || !e.isDir {
		m.setStatus(statusNone, "请选中一个目录后按 D 递归下载")
		return m, nil
	}
	m.pathInput = pathInput{purpose: purposeDownloadDir, target: *e}
	return m.enterInput(InputPath, "保存至: ", "本地目录路径", e.name)
}

// enterInput 进入输入模式并聚焦（prefill 为预填内容，可为空）
func (m *Model) enterInput(mode InputMode, prompt, placeholder, prefill string) (tea.Model, tea.Cmd) {
	m.inputMode = mode
	m.input.Prompt = prompt
	m.input.Placeholder = placeholder
	m.input.SetValue(prefill)
	return m, m.input.Focus()
}

// confirmPathInput 确认下载路径输入
func (m *Model) confirmPathInput(value string) (tea.Model, tea.Cmd) {
	pi := m.pathInput
	value = strings.TrimSpace(value)
	if value == "" {
		return m, nil
	}
	if pi.purpose == purposeDownloadDir {
		m.setBusy("get-dir", pi.target.name)
		return m, downloadDirCmd(m.getter, m.currentBucket, pi.target.key, value)
	}
	m.transferCh = make(chan tea.Msg, 64)
	m.setBusy("get", pi.target.name)
	return m, downloadCmd(m.getter, m.currentBucket, pi.target.key, value, m.transferCh)
}

// openDeleteConfirm 删除确认（单对象/递归目录，spec 4.8）
func (m *Model) openDeleteConfirm() (tea.Model, tea.Cmd) {
	e := m.objects.current()
	if e == nil || e.isBack {
		return m, nil
	}

	var body []string
	target := m.currentBucket + "/" + e.key
	if e.isDir {
		body = []string{
			shorten(target, 52),
			"",
			"将递归删除该目录下所有对象，",
			"此操作不可恢复。",
		}
	} else {
		body = []string{
			shorten(target, 52),
			"",
			"此操作不可恢复。",
		}
	}

	key := e.key
	isDir := e.isDir
	m.activeModal = &modal{
		kind:    ModalConfirm,
		title:   "删除确认",
		body:    body,
		targets: []entry{*e},
		onYes: func(mm *Model) tea.Cmd {
			mm.closeModal()
			mm.setBusy(opOf(isDir), key)
			if isDir {
				return deleteDirCmd(mm.deleter, mm.currentBucket, key)
			}
			return deleteCmd(mm.deleter, mm.currentBucket, key)
		},
	}
	return m, nil
}

// openBatchDeleteConfirm 批量删除确认（spec 4.9）
func (m *Model) openBatchDeleteConfirm() (tea.Model, tea.Cmd) {
	targets := m.objects.selectedEntries()
	if len(targets) == 0 {
		m.setStatus(statusNone, "先用 Space 标记多个对象，再按 X 批量删除")
		return m, nil
	}

	body := []string{sprintf("共选中 %d 个对象：", len(targets))}
	for i, t := range targets {
		if i >= 10 {
			body = append(body, sprintf("  … 其余 %d 个", len(targets)-10))
			break
		}
		body = append(body, "  "+shorten(m.currentBucket+"/"+t.key, 50))
	}
	body = append(body, "", "此操作不可恢复。")

	m.activeModal = &modal{
		kind:    ModalConfirm,
		title:   "批量删除",
		body:    body,
		targets: targets,
		onYes: func(mm *Model) tea.Cmd {
			mm.closeModal()
			mm.setBusy("del", sprintf("%d 个对象", len(targets)))
			return deleteBatchCmd(mm.deleter, mm.currentBucket, targets)
		},
	}
	return m, nil
}

// copyCurrentKey 复制当前对象完整 Key 到剪贴板
func (m *Model) copyCurrentKey() (tea.Model, tea.Cmd) {
	e := m.objects.current()
	if e == nil || e.isBack {
		return m, nil
	}
	fullKey := m.currentBucket + "/" + e.key
	if err := copyToClipboard(fullKey); err != nil {
		m.setStatus(statusErr, "✗ 复制失败: %s", shorten(err.Error(), 56))
	} else {
		m.setStatus(statusOk, "✓ 已复制: %s", shorten(fullKey, 56))
	}
	return m, nil
}

// opOf 目录/文件对应的删除操作名
func opOf(isDir bool) string {
	if isDir {
		return "del-dir"
	}
	return "del"
}

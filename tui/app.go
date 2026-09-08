package tui

import (
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/minio/minio-go/v7"
)

// PersistCurrentContext 可选：用于 set-default 落盘的全局回调
var PersistCurrentContext func(name string) error

// Run 启动 TUI
// contextName 为启动时使用的 context 名称
// onContextChange 为 TUI 内切换 context 时调用的回调（参数：新 context 名称）
// readOnly 为 true 时表示 conf 不可写，标题加 (readonly) 后缀、set-default 命令被拒绝
func Run(client *minio.Client, contextName string, onContextChange func(name string) (newClient *minio.Client, newCore *minio.Core, err error), readOnly bool) error {
	m := NewModel(client)
	m.contextName = contextName
	m.onContextChange = onContextChange
	m.readOnly = readOnly
	p := tea.NewProgram(&m)
	_, err := p.Run()
	return err
}

// Init 实现 tea.Model：加载桶列表 + 启动 spinner
func (m *Model) Init() tea.Cmd {
	return tea.Batch(loadBucketsCmd(m.lister), m.spinner.Tick)
}

// Update 实现 tea.Model
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)

	case spinner.TickMsg:
		if m.loading || m.transfer != nil {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case bucketsLoadedMsg:
		return m.handleBucketsLoaded(msg)

	case listStreamMsg:
		return m.handleListStream(msg)

	case objectInfoLoadedMsg:
		return m.handleMetaLoaded(msg)

	case previewLoadedMsg:
		return m.handlePreviewLoaded(msg)

	case operationCompleteMsg:
		return m.handleOperationComplete(msg)

	case transferProgressMsg:
		return m.handleTransferProgress(msg)

	case contextSwitchedMsg:
		return m.handleContextSwitched(msg)
	}

	return m.forwardToComponents(msg)
}

// handleTransferProgress 更新传输中的真实字节进度
func (m *Model) handleTransferProgress(msg transferProgressMsg) (tea.Model, tea.Cmd) {
	if m.transfer == nil {
		m.setBusy(msg.op, msg.object)
	}
	m.transfer.op = msg.op
	m.transfer.object = msg.object
	m.transfer.label = busyLabel(msg.op, msg.object)
	m.transfer.doneBytes = msg.doneBytes
	m.transfer.totalBytes = msg.totalBytes
	if m.transfer.startedAt.IsZero() {
		m.transfer.startedAt = nowFn()
	}
	if m.transferCh == nil {
		return m, nil
	}
	return m, waitTransferMsg(m.transferCh)
}

// resize 终端尺寸变化时重排布局
func (m *Model) resize(w, h int) {
	m.width, m.height = w, h
	mainH := mainAreaHeight(h)

	m.buckets.rows = maxInt(mainH-1, 1)
	m.objects.rows = maxInt(mainH-2, 1)
	m.input.SetWidth(maxInt(w-16, 10))
	if m.pickerActive {
		m.picker.SetHeight(m.pickerModalHeight())
	}

	if m.preview != nil {
		listRows := maxInt(mainH*previewListRatio/100, 2)
		m.objects.rows = maxInt(listRows-2, 1)
		previewRows := maxInt(mainH-listRows-2, 1)
		m.preview.setBounds(previewRows, maxInt(w-9, 1))
	}
}

// 布局常量
const (
	previewListRatio = 40 // 预览时对象列表占比（spec 4.4）
	leftPaneRatio    = 20 // 左侧桶面板宽度占比
)

// mainAreaHeight 主区域高度（除去标题/分隔/操作栏/状态栏）
func mainAreaHeight(h int) int { return h - 5 }

// leftPaneWidth 左面板宽度
func leftPaneWidth(w int) int { return w * leftPaneRatio / 100 }

// handleBucketsLoaded 桶列表加载完成
func (m *Model) handleBucketsLoaded(msg bucketsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.loading = false
		m.activeModal = &modal{
			kind:  ModalError,
			title: "连接失败",
			body: []string{
				"无法连接 S3 端点或加载桶列表失败：",
				shorten(msg.err.Error(), 60),
				"",
				"请检查配置：",
				"  s3m context list            查看已有 context",
				"  s3m context set <name>      交互式创建/更新",
				"  s3m context current <name>  设置默认 context",
			},
		}
		return m, nil
	}

	items := make([]entry, 0, len(msg.buckets))
	for _, b := range msg.buckets {
		items = append(items, entry{
			key:      b.Name,
			name:     b.Name,
			isBucket: true,
			isDir:    true,
			modified: b.CreationDate,
		})
	}
	m.buckets.setItems(items)
	m.loading = false
	return m, nil
}

// handleListStream 处理对象列表流式批次
func (m *Model) handleListStream(msg listStreamMsg) (tea.Model, tea.Cmd) {
	// 过期批次（已发起新的加载）直接丢弃
	if msg.gen != m.loadGen {
		return m, nil
	}
	if msg.err != nil {
		m.loading = false
		m.openError("加载对象失败", msg.err)
		return m, nil
	}

	m.appendObjectBatch(msg)
	if msg.done {
		m.loading = false
		return m, nil
	}
	return m, readNextStream(m.loadingCh)
}

// readNextStream 读取下一批流式结果
func readNextStream(ch chan listStreamMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// handleMetaLoaded Meta 信息加载完成 -> 打开弹窗
func (m *Model) handleMetaLoaded(msg objectInfoLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.openError("查询对象状态失败", msg.err)
		return m, nil
	}
	m.activeModal = &modal{kind: ModalMeta, meta: newObjectMeta(msg.info)}
	return m, nil
}

// handlePreviewLoaded 预览内容加载完成
func (m *Model) handlePreviewLoaded(msg previewLoadedMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if m.preview == nil || m.preview.key != msg.key {
		return m, nil // 已关闭或过期
	}
	if msg.err != nil {
		m.closePreview()
		m.openError("加载预览失败", msg.err)
		return m, nil
	}
	m.preview.setContent(msg.lines, msg.size, msg.truncated)
	m.resize(m.width, m.height)
	return m, nil
}

// handleOperationComplete 异步操作完成
func (m *Model) handleOperationComplete(msg operationCompleteMsg) (tea.Model, tea.Cmd) {
	m.clearBusy()

	result := "success"
	if msg.err != nil {
		result = msg.err.Error()
	}
	m.AddHistory(opLabel(msg.op), msg.object, result)

	if msg.err != nil {
		m.setStatus(statusErr, "✗ %s %s: %s", opLabel(msg.op), shorten(msg.object, 40), shorten(msg.err.Error(), 48))
	} else {
		text := "✓ " + opLabel(msg.op) + " " + shorten(msg.object, 48)
		if msg.details != "" {
			text += " (" + msg.details + ")"
		}
		m.setStatus(statusOk, "%s", text)
	}

	if msg.refresh && m.inBucket() {
		return m, m.startObjectLoad()
	}
	return m, nil
}

// handleContextSwitched context 切换完成
func (m *Model) handleContextSwitched(msg contextSwitchedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.AddHistory("use", msg.name, "error: "+msg.err.Error())
		m.setStatus(statusErr, "✗ 切换 context 失败: %s", shorten(msg.err.Error(), 56))
		return m, nil
	}
	m.applyClient(msg.newClient, msg.newCore, msg.name)
	m.AddHistory("use", msg.name, "switched (persist="+boolStr(msg.persist)+")")
	m.currentBucket = ""
	m.currentPrefix = ""
	m.focus = FocusBuckets
	m.objects.setItems(nil)
	m.loading = true
	return m, tea.Batch(loadBucketsCmd(m.lister), m.spinner.Tick)
}

// forwardToComponents 将消息转发给持有焦点的子组件
func (m *Model) forwardToComponents(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if m.inputMode != InputNone {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}
	if m.pickerActive {
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// opLabel 操作中文名
func opLabel(op string) string {
	switch op {
	case "get":
		return "下载"
	case "get-dir":
		return "下载目录"
	case "put":
		return "上传"
	case "put-dir":
		return "上传目录"
	case "del":
		return "删除"
	case "del-dir":
		return "删除目录"
	case "sign":
		return "签名"
	default:
		return op
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// overlayCenter 将 box 叠加居中渲染在 base 之上（ANSI 感知）
func overlayCenter(base, box string, width int) string {
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")

	boxH, boxW := len(boxLines), maxLineWidth(box)
	top := (len(baseLines) - boxH) / 2
	left := (width - boxW) / 2
	if top < 0 {
		top = 0
	}
	if left < 0 {
		left = 0
	}

	for i := range baseLines {
		bi := i - top
		if bi < 0 || bi >= boxH {
			continue
		}
		boxLine := boxLines[bi]
		head := ansi.Truncate(baseLines[i], left, "")
		tail := ansi.TruncateLeft(baseLines[i], left+ansi.StringWidth(boxLine), "")
		baseLines[i] = head + boxLine + tail
	}
	return strings.Join(baseLines, "\n")
}

// maxLineWidth 多行字符串的最大显示宽度
func maxLineWidth(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		if w := ansi.StringWidth(line); w > max {
			max = w
		}
	}
	return max
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func waitTransferMsg(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

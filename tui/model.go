package tui

import (
	"time"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/minio/minio-go/v7"
	"s3m/operations"
)

// Model TUI 主模型（状态机见 spec/tui.md 第七节）
type Model struct {
	client  *minio.Client
	core    *minio.Core
	lister  *operations.Lister
	stater  *operations.Stater
	getter  *operations.Getter
	putter  *operations.Putter
	signer  *operations.Signer
	deleter *operations.Deleter

	// context
	contextName     string
	onContextChange func(name string) (newClient *minio.Client, newCore *minio.Core, err error)
	readOnly        bool

	// 界面状态
	focus        FocusPane
	inputMode    InputMode
	activeModal  *modal
	preview      *previewState
	pickerActive bool
	pickerDir    bool // true = 选目录上传，false = 选文件上传
	pathInput    pathInput

	// 数据面板
	buckets pane
	objects pane

	currentBucket string // 当前桶（空 = 桶列表根视图）
	currentPrefix string // 当前前缀

	// 加载/任务状态
	loading     bool
	loadingCh   chan listStreamMsg
	loadingStop chan struct{}
	loadGen     int // 流式加载代次，用于丢弃过期批次
	transfer    *transferState
	transferCh  chan tea.Msg
	statusText  string
	statusKind  statusKind
	err         error

	width  int
	height int

	// 历史
	history    []HistoryEntry
	maxHistory int

	// 组件
	input   textinput.Model
	picker  filepicker.Model
	spinner spinner.Model
}

// NewModel 创建 TUI 模型
func NewModel(client *minio.Client) Model {
	m := Model{
		client:     client,
		lister:     operations.NewLister(client),
		stater:     operations.NewStater(client),
		getter:     operations.NewGetter(client),
		putter:     operations.NewPutter(client),
		signer:     operations.NewSigner(client),
		deleter:    operations.NewDeleter(client),
		focus:      FocusBuckets,
		inputMode:  InputNone,
		loading:    true, // 启动即加载桶列表
		maxHistory: 50,
	}

	m.input = textinput.New()
	m.input.Prompt = ": "
	m.input.CharLimit = 1024

	m.picker = filepicker.New()
	m.picker.FileAllowed = true
	m.picker.DirAllowed = false
	m.picker.ShowSize = true
	// Esc 保留给关闭弹窗，返回上级用 h/←
	m.picker.KeyMap.Back = filepicker.DefaultKeyMap().Back
	m.picker.Styles.Cursor = paneFocusMarkStyle
	m.picker.Styles.Selected = markStyle

	m.spinner = spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(spinnerStyle))

	return m
}

// AddHistory 添加操作历史（最新在前）
func (m *Model) AddHistory(op, object, result string) {
	m.history = append([]HistoryEntry{{
		Timestamp: time.Now(), Operation: op, Object: object, Result: result,
	}}, m.history...)
	if len(m.history) > m.maxHistory {
		m.history = m.history[:m.maxHistory]
	}
}

// applyClient 切换 context 后重建各操作器
func (m *Model) applyClient(newClient *minio.Client, newCore *minio.Core, name string) {
	m.client = newClient
	m.core = newCore
	m.contextName = name
	m.lister = operations.NewLister(newClient)
	m.stater = operations.NewStater(newClient)
	m.getter = operations.NewGetter(newClient)
	m.putter = operations.NewPutter(newClient)
	m.signer = operations.NewSigner(newClient)
	m.deleter = operations.NewDeleter(newClient)
}

// setStatus 设置状态栏消息
func (m *Model) setStatus(kind statusKind, format string, args ...interface{}) {
	m.statusKind = kind
	m.statusText = sprintf(format, args...)
}

// clearStatus 清除状态栏消息
func (m *Model) clearStatus() {
	m.statusKind = statusNone
	m.statusText = ""
}

// setBusy 标记进行中的任务
func (m *Model) setBusy(op, object string) {
	m.transfer = &transferState{
		op:        op,
		object:    object,
		label:     busyLabel(op, object),
		startedAt: nowFn(),
	}
}

// clearBusy 清除任务标记
func (m *Model) clearBusy() {
	m.transfer = nil
	m.transferCh = nil
}

// closeModal 关闭当前弹窗
func (m *Model) closeModal() {
	m.activeModal = nil
}

// openError 打开错误弹窗
func (m *Model) openError(title string, err error) {
	m.activeModal = &modal{
		kind:  ModalError,
		title: title,
		body:  []string{err.Error()},
	}
}

// inBucket 是否处于某个桶内
func (m *Model) inBucket() bool {
	return m.currentBucket != ""
}

// openBucket 进入指定桶并加载对象列表
func (m *Model) openBucket(bucket string) tea.Cmd {
	m.currentBucket = bucket
	m.currentPrefix = ""
	return m.startObjectLoadReset()
}

// startObjectLoad 重新加载当前桶/前缀下的对象（流式）
func (m *Model) startObjectLoad() tea.Cmd {
	m.stopStreaming() // 中止旧的流式加载
	m.loadGen++
	m.objects.setItems(nil)
	m.objects.clearSelection()
	m.loading = true
	m.loadingCh = make(chan listStreamMsg, 16)
	m.loadingStop = make(chan struct{})

	return tea.Batch(
		loadObjectsCmd(m.lister, m.currentBucket, m.currentPrefix, m.loadGen, m.loadingCh, m.loadingStop),
		m.spinner.Tick,
	)
}

// startObjectLoadReset 加载并重置过滤词（用于切换目录）
func (m *Model) startObjectLoadReset() tea.Cmd {
	m.objects.setFilter("")
	return m.startObjectLoad()
}

// stopStreaming 中止进行中的流式加载
func (m *Model) stopStreaming() {
	if m.loadingStop != nil {
		close(m.loadingStop)
		m.loadingStop = nil
	}
}

// refreshActive 刷新当前焦点对应列表
func (m *Model) refreshActive() tea.Cmd {
	if m.focus == FocusBuckets || !m.inBucket() {
		return loadBucketsCmd(m.lister)
	}
	return m.startObjectLoad()
}

// appendObjectBatch 追加流式批次到对象面板（批次内排序后与已有列表归并）
func (m *Model) appendObjectBatch(msg listStreamMsg) {
	items := m.objects.items
	if msg.first {
		items = nil
	}
	batch := append([]entry(nil), msg.items...)
	sortEntries(batch)
	items = withBackRow(items)
	items = mergeSortedEntries(items, batch)
	m.objects.setItems(items)
}

// mergeSortedEntries 归并两个按 entryLess 排序的列表（back 行最前）
func mergeSortedEntries(a, b []entry) []entry {
	out := make([]entry, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if entryLess(a[i], b[j]) {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

// withBackRow 确保首行是返回行
func withBackRow(items []entry) []entry {
	if len(items) > 0 && items[0].isBack {
		return items
	}
	return append([]entry{newBackEntry()}, items...)
}

// openPreview 打开文本预览（带大文件策略检查），返回加载命令（可能为 nil）
func (m *Model) openPreview(e entry) tea.Cmd {
	if e.isDir {
		return nil
	}
	if e.size > previewRejectSize {
		m.activeModal = &modal{
			kind:  ModalError,
			title: "文件过大",
			body: []string{
				shorten(e.name, 46) + " (" + formatSize(e.size) + ")",
				"",
				"超过 10 MB，不建议预览。",
				"请使用命令行: s3m cat " + m.currentBucket + "/" + e.key,
			},
		}
		return nil
	}
	if e.size > previewWarnSize {
		key := e.key
		m.activeModal = &modal{
			kind:  ModalConfirm,
			title: "文件较大",
			body: []string{
				shorten(e.name, 46) + " (" + formatSize(e.size) + ")",
				"",
				"仅显示前 " + itoa(previewMaxLines) + " 行？",
			},
			onYes: func(mm *Model) tea.Cmd {
				mm.closeModal()
				return mm.beginPreview(key)
			},
		}
		return nil
	}
	return m.beginPreview(e.key)
}

// beginPreview 异步加载预览内容
func (m *Model) beginPreview(key string) tea.Cmd {
	m.preview = newPreviewState(key)
	m.loading = true
	return tea.Batch(loadPreviewCmd(m.getter, m.currentBucket, key), m.spinner.Tick)
}

// closePreview 关闭预览
func (m *Model) closePreview() {
	m.preview = nil
}

// goUp 返回上一级（对象面板）
func (m *Model) goUp() tea.Cmd {
	if !m.inBucket() {
		return nil
	}
	if m.currentPrefix == "" {
		// 桶根 -> 桶列表
		m.currentBucket = ""
		m.focus = FocusBuckets
		m.objects.setItems(nil)
		return loadBucketsCmd(m.lister)
	}
	m.currentPrefix = parentPrefix(m.currentPrefix)
	return m.startObjectLoadReset()
}

// goRoot 回到桶根目录
func (m *Model) goRoot() tea.Cmd {
	if !m.inBucket() || m.currentPrefix == "" {
		return nil
	}
	m.currentPrefix = ""
	return m.startObjectLoadReset()
}

// enterEntry 处理 Enter/→/l：进目录或预览文本文件
func (m *Model) enterEntry() tea.Cmd {
	e := m.objects.current()
	if e == nil {
		return nil
	}
	switch {
	case e.isBack:
		return m.goUp()
	case e.isDir:
		m.currentPrefix = e.key
		return m.startObjectLoadReset()
	case isTextEntry(*e):
		return m.openPreview(*e)
	default:
		m.setStatus(statusNone, "二进制文件不支持预览，按 i 查看信息 / d 下载")
		return nil
	}
}

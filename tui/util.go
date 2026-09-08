package tui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

var nowFn = time.Now

// sprintf 便捷格式化
func sprintf(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}

// itoa 便捷整型转字符串
func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// formatSize 字节数格式化为 KiB 单位（spec 示例 4.2 KiB）
func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < 0 {
		return "-"
	}
	if bytes < unit {
		return itoa(int(bytes)) + " B"
	}
	sizes := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit && exp < len(sizes)-1; n /= unit {
		div *= unit
		exp++
	}
	val := float64(bytes) / float64(div)
	if val >= 100 {
		return fmt.Sprintf("%.0f %s", math.Round(val), sizes[exp])
	}
	return fmt.Sprintf("%.1f %s", val, sizes[exp])
}

// parentPrefix 计算上一级前缀："a/b/" -> "a/"，"a/" -> ""
func parentPrefix(prefix string) string {
	p := strings.TrimSuffix(prefix, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i+1]
	}
	return ""
}

// textExts 可预览的文本扩展名
var textExts = map[string]bool{
	".txt": true, ".log": true, ".md": true, ".json": true, ".yaml": true,
	".yml": true, ".xml": true, ".csv": true, ".tsv": true, ".conf": true,
	".ini": true, ".toml": true, ".cfg": true, ".env": true, ".properties": true,
	".sql": true, ".sh": true, ".bash": true, ".zsh": true, ".py": true,
	".go": true, ".rs": true, ".java": true, ".c": true, ".h": true,
	".cpp": true, ".hpp": true, ".js": true, ".ts": true, ".jsx": true,
	".tsx": true, ".html": true, ".htm": true, ".css": true, ".scss": true,
	".rb": true, ".php": true, ".lua": true, ".pl": true, ".proto": true,
	".tf": true, ".service": true, "Makefile": true, "Dockerfile": true,
	".gitignore": true, ".gitattributes": true, ".lock": true, ".mod": true,
	".sum": true,
}

// isTextEntry 判断对象是否可按文本预览（扩展名或 ContentType 判定）
func isTextEntry(e entry) bool {
	if e.isDir || e.isBucket {
		return false
	}
	lower := strings.ToLower(e.name)
	if i := strings.LastIndex(lower, "/"); i >= 0 {
		lower = lower[i+1:]
	}
	for ext, ok := range textExts {
		if ok && strings.HasSuffix(lower, strings.ToLower(ext)) {
			return true
		}
	}
	return false
}

// shorten 按显示宽度截断字符串（超长加省略号）
func shorten(s string, max int) string {
	if max <= 0 || displayWidth(s) <= max {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > max-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

// padRight 按显示宽度右补空格（ANSI 感知）
func padRight(s string, width int) string {
	gap := width - displayWidth(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

// padLeft 按显示宽度左补空格（右对齐，ANSI 感知）
func padLeft(s string, width int) string {
	gap := width - displayWidth(s)
	if gap <= 0 {
		return s
	}
	return strings.Repeat(" ", gap) + s
}

// truncateWidth 按显示宽度截断（不加省略号，ANSI 感知）
func truncateWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "")
}

// displayWidth 计算显示宽度（ANSI 感知，东亚字符占 2 列）
func displayWidth(s string) int {
	return ansi.StringWidth(s)
}

// runeWidth 单字符宽度
func runeWidth(r rune) int {
	if r == '…' {
		return 1
	}
	return runewidth.RuneWidth(r)
}

// copyToClipboard 复制文本到剪贴板
func copyToClipboard(text string) error {
	return clipboard.WriteAll(text)
}

// isLocalDir 判断本地路径是否为目录
func isLocalDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// rowSep 行分隔符
func rowSep(width int) string {
	return strings.Repeat("─", width)
}

func clampInt(n, min, max int) int {
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func formatRate(bytesPerSecond float64) string {
	if bytesPerSecond <= 0 {
		return "-"
	}
	return formatSize(int64(bytesPerSecond)) + "/s"
}

func formatETA(seconds float64) string {
	if seconds <= 0 || math.IsInf(seconds, 0) || math.IsNaN(seconds) {
		return "-"
	}
	d := time.Duration(seconds * float64(time.Second))
	if d < time.Second {
		return "1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Round(time.Second)/time.Second))
	}
	mins := int(d / time.Minute)
	secs := int((d % time.Minute) / time.Second)
	if secs == 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dm%ds", mins, secs)
}

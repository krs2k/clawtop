package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

const (
	sysInterval    = time.Second
	claudeInterval = time.Minute
)

var (
	green   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellow  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	red     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	cyan    = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	dim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	bracket = lipgloss.NewStyle().Bold(true)
)

type diskInfo struct {
	Mount       string
	Used, Total uint64
	Percent     float64
}

type sysMsg struct {
	cpu    []float64
	mem    *mem.VirtualMemoryStat
	swap   *mem.SwapMemoryStat
	disks  []diskInfo
	load   *load.AvgStat
	agents agentCounts
}

type tickSys struct{}
type tickClaude struct{}

type model struct {
	width  int
	sys    sysMsg
	claude claudeMsg
	cUpd   time.Time
}

func collectSys() tea.Msg {
	var m sysMsg
	// First call with 0 interval returns usage since previous call; we tick every second.
	m.cpu, _ = cpu.Percent(0, true)
	m.mem, _ = mem.VirtualMemory()
	m.swap, _ = mem.SwapMemory()
	m.load, _ = load.Avg()
	m.agents = countAgents()
	parts, _ := disk.Partitions(false)
	seen := map[string]bool{}
	for _, p := range parts {
		if seen[p.Device] || strings.HasPrefix(p.Mountpoint, "/snap") || strings.HasPrefix(p.Mountpoint, "/boot") {
			continue
		}
		switch p.Fstype {
		case "squashfs", "tmpfs", "devtmpfs", "overlay", "efivarfs":
			continue
		}
		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		seen[p.Device] = true
		m.disks = append(m.disks, diskInfo{p.Mountpoint, u.Used, u.Total, u.UsedPercent})
	}
	return m
}

func (m model) Init() tea.Cmd {
	cpu.Percent(0, true) // prime the per-CPU delta
	return tea.Batch(collectSys, func() tea.Msg { return fetchClaudeUsage() })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "r":
			return m, func() tea.Msg { return fetchClaudeUsage() }
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case sysMsg:
		m.sys = msg
		return m, tea.Tick(sysInterval, func(time.Time) tea.Msg { return tickSys{} })
	case tickSys:
		return m, collectSys
	case claudeMsg:
		// Keep the last good values if a refresh fails.
		if msg.err == nil || m.claude.limits == nil {
			m.claude = msg
		} else {
			m.claude.err = msg.err
		}
		if msg.err == nil {
			m.cUpd = time.Now()
		}
		return m, tea.Tick(claudeInterval, func(time.Time) tea.Msg { return tickClaude{} })
	case tickClaude:
		return m, func() tea.Msg { return fetchClaudeUsage() }
	}
	return m, nil
}

// bar renders an htop-style meter: Label[|||||||      42.0%]
func bar(label string, labelW, width int, pct float64, text string) string {
	inner := width - labelW - 2
	if inner < 5 {
		inner = 5
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct / 100 * float64(inner))
	if len(text) > inner {
		text = text[len(text)-inner:]
	}
	textStart := inner - len(text)

	var b strings.Builder
	for i := 0; i < inner; i++ {
		var ch string
		if i >= textStart {
			ch = string(text[i-textStart])
		} else if i < filled {
			ch = "|"
		} else {
			ch = " "
		}
		if i < filled {
			frac := float64(i) / float64(inner)
			switch {
			case frac < 0.6:
				ch = green.Render(ch)
			case frac < 0.85:
				ch = yellow.Render(ch)
			default:
				ch = red.Render(ch)
			}
		} else if i >= textStart {
			ch = dim.Render(ch)
		}
		b.WriteString(ch)
	}
	return cyan.Render(fmt.Sprintf("%-*s", labelW, label)) + bracket.Render("[") + b.String() + bracket.Render("]")
}

func human(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(b)/float64(div), "KMGTPE"[exp])
}

func until(t time.Time) string {
	d := time.Until(t).Round(time.Minute)
	if d <= 0 {
		return "teraz"
	}
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	mm := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, h)
	}
	return fmt.Sprintf("%dh %02dm", h, mm)
}

const maxLines = 15

type meter struct {
	label string
	pct   float64
	text  string
}

// grid lays meters out in cols columns (column-major, like htop's CPU meters).
func grid(items []meter, cols, width int) []string {
	if len(items) == 0 {
		return nil
	}
	if cols < 1 {
		cols = 1
	}
	if cols > len(items) {
		cols = len(items)
	}
	labelW := 0
	for _, it := range items {
		if l := len([]rune(it.label)) + 1; l > labelW {
			labelW = l
		}
	}
	colW := (width - (cols - 1)) / cols
	rows := (len(items) + cols - 1) / cols
	var out []string
	for r := 0; r < rows; r++ {
		var line []string
		for c := 0; c < cols; c++ {
			if i := c*rows + r; i < len(items) {
				line = append(line, bar(items[i].label, labelW, colW, items[i].pct, items[i].text))
			}
		}
		out = append(out, strings.Join(line, " "))
	}
	return out
}

func (m model) View() string {
	w := m.width
	if w == 0 {
		w = 80
	}
	// Max meters per row that still leave each bar readable.
	maxCols := w / 26
	if maxCols < 1 {
		maxCols = 1
	}
	pick := func(n int) int { // columns for a small group, at most 2
		if n > 1 && maxCols >= 2 {
			return 2
		}
		return 1
	}

	var memM []meter
	if vm := m.sys.mem; vm != nil {
		used := vm.Total - vm.Available
		memM = append(memM, meter{"Mem", float64(used) / float64(vm.Total) * 100, human(used) + "/" + human(vm.Total)})
	}
	if sw := m.sys.swap; sw != nil && sw.Total > 0 {
		memM = append(memM, meter{"Swp", sw.UsedPercent, human(sw.Used) + "/" + human(sw.Total)})
	}
	var diskM []meter
	for _, d := range m.sys.disks {
		diskM = append(diskM, meter{d.Mount, d.Percent, human(d.Used) + "/" + human(d.Total)})
	}
	var claudeM []meter
	for _, l := range m.claude.limits {
		claudeM = append(claudeM, meter{l.Name, l.Percent, fmt.Sprintf("%.0f%% reset %s", l.Percent, until(l.ResetsAt))})
	}

	lower := grid(memM, pick(len(memM)), w)
	lower = append(lower, grid(diskM, pick(len(diskM)), w)...)
	lower = append(lower, grid(claudeM, pick(len(claudeM)), w)...)

	// Status line: load avg, Claude state, keys.
	var st []string
	if l := m.sys.load; l != nil {
		st = append(st, fmt.Sprintf("load %.2f %.2f %.2f", l.Load1, l.Load5, l.Load15))
	}
	if a := m.sys.agents; a.busy+a.waiting+a.idle > 0 {
		st = append(st, fmt.Sprintf("agenci: %d pracuje · %d czeka · %d idle", a.busy, a.waiting, a.idle))
	}
	switch {
	case m.claude.err != nil:
		st = append(st, red.Render("claude: "+m.claude.err.Error()))
	case m.claude.limits == nil:
		st = append(st, "claude: ładowanie...")
	default:
		st = append(st, "claude "+m.cUpd.Format("15:04"))
	}
	st = append(st, "q wyjście  r odśwież")
	status := dim.Render(strings.Join(st, " · "))

	// CPU gets whatever rows are left, spread over as many columns as needed.
	n := len(m.sys.cpu)
	cpuRows := maxLines - len(lower) - 1
	if cpuRows < 1 {
		cpuRows = 1
	}
	cols := (n + cpuRows - 1) / cpuRows
	if n > 4 && cols < 2 && maxCols >= 2 {
		cols = 2
	}
	if cols > maxCols {
		cols = maxCols
	}
	cpuM := make([]meter, n)
	for i, p := range m.sys.cpu {
		cpuM[i] = meter{fmt.Sprint(i), p, fmt.Sprintf("%.1f%%", p)}
	}

	lines := append(grid(cpuM, cols, w), lower...)
	if len(lines) > maxLines-1 {
		lines = lines[:maxLines-1]
	}
	lines = append(lines, status)
	return strings.Join(lines, "\n")
}

func main() {
	if _, err := tea.NewProgram(model{}).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

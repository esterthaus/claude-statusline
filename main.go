package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

// ANSI Farben
const (
	Reset         = "\033[0m"
	Dim           = "\033[2m"
	Bold          = "\033[1m"
	Cyan          = "\033[36m"
	Yellow        = "\033[33m"
	Blue          = "\033[34m"
	BrightCyan    = "\033[96m"
	BrightMagenta = "\033[95m"
	BrightYellow  = "\033[93m"
	BrightGreen   = "\033[92m"

	// Ampel-System
	DarkGreen = "\033[32m"
	LightGreen = "\033[92m"
	Amber      = "\033[93m"
	Orange     = "\033[38;5;208m"
	BrightRed  = "\033[91m"
)

// Icons
const (
	IconBrain    = "🧠"
	IconClock    = "⏱"
	IconCalendar = "📅"
	IconFolder   = "📂"
	IconGit      = "⎇"
	IconCPU      = "💻"
	IconRAM      = "🎛"
	IconSession  = "⏳"
	IconCost     = "💰"
	IconWorktree = "🌿"
)

// Input JSON Struktur (von Claude Code)
type StatusLineInput struct {
	ContextWindow struct {
		CurrentUsage *struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"current_usage"`
		TotalInputTokens    int  `json:"total_input_tokens"`
		TotalOutputTokens   int  `json:"total_output_tokens"`
		ContextWindowSize   int  `json:"context_window_size"`
		UsedPercentage      *float64 `json:"used_percentage"`
		RemainingPercentage *float64 `json:"remaining_percentage"`
	} `json:"context_window"`
	Model struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
		ProjectDir string `json:"project_dir"`
	} `json:"workspace"`
	Cost *struct {
		TotalCostUSD       float64 `json:"total_cost_usd"`
		TotalDurationMs    int64   `json:"total_duration_ms"`
		TotalAPIDurationMs int64   `json:"total_api_duration_ms"`
		TotalLinesAdded    int     `json:"total_lines_added"`
		TotalLinesRemoved  int     `json:"total_lines_removed"`
	} `json:"cost"`
	Worktree *struct {
		Name           string `json:"name"`
		Path           string `json:"path"`
		Branch         string `json:"branch"`
		OriginalCwd    string `json:"original_cwd"`
		OriginalBranch string `json:"original_branch"`
	} `json:"worktree"`
	Vim *struct {
		Mode string `json:"mode"`
	} `json:"vim"`
	Agent *struct {
		Name string `json:"name"`
	} `json:"agent"`
	RateLimits *struct {
		FiveHour *struct {
			UsedPercentage float64 `json:"used_percentage"`
			ResetsAt       float64 `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay *struct {
			UsedPercentage float64 `json:"used_percentage"`
			ResetsAt       float64 `json:"resets_at"`
		} `json:"seven_day"`
	} `json:"rate_limits"`
	Version           string `json:"version"`
	SessionID         string `json:"session_id"`
	Cwd               string `json:"cwd"`
	Exceeds200kTokens bool   `json:"exceeds_200k_tokens"`
}

// UsageData fuer Rate-Limit-Anzeige
type UsageData struct {
	FiveHourUtil   int
	SevenDayUtil   int
	FiveHourReset  int64
	SevenDayReset  int64
}

func main() {
	// JSON von stdin lesen
	reader := bufio.NewReader(os.Stdin)
	var inputBuilder strings.Builder
	for {
		line, err := reader.ReadString('\n')
		inputBuilder.WriteString(line)
		if err != nil {
			break
		}
	}

	var input StatusLineInput
	if err := json.Unmarshal([]byte(inputBuilder.String()), &input); err != nil {
		// Fallback bei Parse-Fehler
		fmt.Println(Dim + "Statusline: JSON parse error" + Reset)
		fmt.Println(Dim + "Error: " + err.Error() + Reset)
		return
	}

	// Daten extrahieren
	modelName := input.Model.DisplayName
	if modelName == "" {
		modelName = "Unknown"
	}

	cwd := input.Workspace.CurrentDir
	if cwd == "" {
		cwd = input.Cwd
	}
	if cwd == "" {
		cwd = "~"
	}
	cwd = shortenPath(cwd)

	// Context Window: native used_percentage nutzen, Fallback auf manuelle Berechnung
	var currentTokens int
	contextSize := input.ContextWindow.ContextWindowSize
	if contextSize == 0 {
		contextSize = 200000
	}

	if input.ContextWindow.UsedPercentage != nil {
		currentTokens = int(*input.ContextWindow.UsedPercentage) * contextSize / 100
	} else if input.ContextWindow.CurrentUsage != nil {
		cu := input.ContextWindow.CurrentUsage
		currentTokens = cu.InputTokens + cu.CacheCreationInputTokens + cu.CacheReadInputTokens
	}

	// Usage Limits aus stdin-JSON (nativ von Claude Code)
	var usage UsageData
	if input.RateLimits != nil {
		if input.RateLimits.FiveHour != nil {
			usage.FiveHourUtil = int(input.RateLimits.FiveHour.UsedPercentage)
			usage.FiveHourReset = int64(input.RateLimits.FiveHour.ResetsAt)
		} else {
			usage.FiveHourUtil = -1
		}
		if input.RateLimits.SevenDay != nil {
			usage.SevenDayUtil = int(input.RateLimits.SevenDay.UsedPercentage)
			usage.SevenDayReset = int64(input.RateLimits.SevenDay.ResetsAt)
		} else {
			usage.SevenDayUtil = -1
		}
	} else {
		usage.FiveHourUtil = -1
		usage.SevenDayUtil = -1
	}

	// Git Status holen
	gitStatus := getGitStatus(cwd)

	// Zeile 1: Modell + Context + Usage Limits
	line1 := fmt.Sprintf("%s%s%s%s  %s•%s  %s",
		BrightMagenta+Bold, modelName, Reset,
		"",
		Dim, Reset,
		renderProgressBarWithValues(currentTokens, contextSize, IconBrain+" Context", BrightCyan, 15),
	)

	// 5h Usage Limit
	line1 += fmt.Sprintf("  %s•%s  ", Dim, Reset)
	if usage.FiveHourUtil >= 0 {
		line1 += renderProgressBar(usage.FiveHourUtil, 100, IconClock+" 5h", 12, BrightMagenta)
		if usage.FiveHourReset > 0 {
			resetTime := time.Unix(usage.FiveHourReset, 0)
			timeUntil := time.Until(resetTime)
			if timeUntil > 0 {
				line1 += fmt.Sprintf(" %sin %s (%s)%s", Dim, formatDuration(timeUntil), resetTime.Local().Format("15:04"), Reset)
			}
		}
	} else {
		line1 += Dim + IconClock + " 5h: N/A" + Reset
	}

	// 7d Usage Limit
	line1 += fmt.Sprintf("  %s•%s  ", Dim, Reset)
	if usage.SevenDayUtil >= 0 {
		line1 += renderProgressBar(usage.SevenDayUtil, 100, IconCalendar+" 7d", 12, BrightYellow)
		if usage.SevenDayReset > 0 {
			resetTime := time.Unix(usage.SevenDayReset, 0)
			line1 += fmt.Sprintf(" %s%s%s", Dim, resetTime.Local().Format("02. Jan 15:04"), Reset)
		}
	} else {
		line1 += Dim + IconCalendar + " 7d: N/A" + Reset
	}

	// System Stats holen
	cpuPercent, memPercent := getSystemStats()
	sessionDur := getSessionDuration()

	// Zeile 2: CWD + Git Status + Worktree
	line2 := fmt.Sprintf("%s %s%s%s  %s•%s  %s %s",
		IconFolder, Cyan, cwd, Reset,
		Dim, Reset,
		IconGit, gitStatus,
	)

	if input.Worktree != nil && input.Worktree.Name != "" {
		line2 += fmt.Sprintf("  %s•%s  %s %s%s%s",
			Dim, Reset,
			IconWorktree, BrightGreen, input.Worktree.Name, Reset,
		)
	}

	// Zeile 3: System Stats + Session
	line3 := fmt.Sprintf("%s CPU: %s %s%.0f%%%s",
		IconCPU, renderMiniBar(cpuPercent, 8),
		getColorForPercentage(int(cpuPercent)), cpuPercent, Reset,
	)
	line3 += fmt.Sprintf("  %s•%s  %s RAM: %s %s%.0f%%%s",
		Dim, Reset,
		IconRAM, renderMiniBar(memPercent, 8),
		getColorForPercentage(int(memPercent)), memPercent, Reset,
	)

	// Session-Dauer
	if sessionDur > 0 {
		line3 += fmt.Sprintf("  %s•%s  %s Session: %s%s%s",
			Dim, Reset,
			IconSession, BrightCyan, formatSessionDuration(sessionDur), Reset,
		)
	} else {
		line3 += fmt.Sprintf("  %s•%s  %s Session: %s<1m%s",
			Dim, Reset,
			IconSession, Dim, Reset,
		)
	}

	// Kosten (nur anzeigen wenn vorhanden)
	if input.Cost != nil && input.Cost.TotalCostUSD > 0 {
		line3 += fmt.Sprintf("  %s•%s  %s %s$%.2f%s",
			Dim, Reset,
			IconCost, BrightYellow, input.Cost.TotalCostUSD, Reset,
		)
	}

	fmt.Println(line1)
	fmt.Println(line2)
	fmt.Println(line3)
}


// formatDuration formatiert eine Dauer als "2h 30m"
func formatDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute

	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// shortenPath kürzt den Pfad (ersetzt Home mit ~)
func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// formatWithKSuffix formatiert Zahlen mit k-Suffix
func formatWithKSuffix(num int) string {
	if num >= 1000 {
		return fmt.Sprintf("%dk", num/1000)
	}
	return strconv.Itoa(num)
}

// getColorForPercentage gibt die Ampel-Farbe für einen Prozentsatz zurück
func getColorForPercentage(percentage int) string {
	switch {
	case percentage > 85:
		return BrightRed
	case percentage > 70:
		return Orange
	case percentage > 50:
		return Amber
	case percentage > 30:
		return LightGreen
	default:
		return DarkGreen
	}
}

// renderProgressBar rendert eine Progress Bar (ohne Werte)
func renderProgressBar(current, max int, label string, width int, labelColor string) string {
	if max == 0 {
		return Dim + label + ": N/A" + Reset
	}

	percentage := current * 100 / max
	filled := current * width / max
	if filled > width {
		filled = width
	}
	empty := width - filled

	color := getColorForPercentage(percentage)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)

	return fmt.Sprintf("%s%s:%s %s%s%s %s%d%%%s",
		labelColor, label, Reset,
		color, bar, Reset,
		color, percentage, Reset,
	)
}

// renderProgressBarWithValues rendert eine Progress Bar mit Werten
func renderProgressBarWithValues(current, max int, label, labelColor string, width int) string {
	if max == 0 {
		return Dim + label + ": N/A" + Reset
	}

	currentFmt := formatWithKSuffix(current)
	maxFmt := formatWithKSuffix(max)

	percentage := current * 100 / max
	filled := current * width / max
	if filled > width {
		filled = width
	}
	empty := width - filled

	color := getColorForPercentage(percentage)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)

	return fmt.Sprintf("%s%s:%s %s/%s %s%s%s %s%d%%%s",
		labelColor, label, Reset,
		currentFmt, maxFmt,
		color, bar, Reset,
		color, percentage, Reset,
	)
}

// getGitStatus holt den Git-Status
func getGitStatus(cwd string) string {
	workDir := resolveWorkDir(cwd)

	// Prüfe ob wir in einem Git-Repository sind
	if !isGitRepo(workDir) {
		return Dim + "Änderungen: N/A | Staged: N/A | Stash: N/A | Unpushed: N/A | Unpulled: N/A" + Reset
	}

	var parts []string

	// 1. Uncommitted changes
	changes := gitCountLines(workDir, "status", "--porcelain")
	if changes > 0 {
		parts = append(parts, fmt.Sprintf("%sÄnderungen: %d%s", Yellow, changes, Reset))
	} else {
		parts = append(parts, Dim+"Änderungen: 0"+Reset)
	}

	// 2. Staged changes
	staged := gitCountLines(workDir, "diff", "--cached", "--numstat")
	if staged > 0 {
		parts = append(parts, fmt.Sprintf("%sStaged: %d%s", BrightGreen, staged, Reset))
	} else {
		parts = append(parts, Dim+"Staged: 0"+Reset)
	}

	// 3. Stash count
	stashCount := gitCountLines(workDir, "stash", "list")
	if stashCount > 0 {
		parts = append(parts, fmt.Sprintf("%sStash: %d%s", Cyan, stashCount, Reset))
	} else {
		parts = append(parts, Dim+"Stash: 0"+Reset)
	}

	// 4. Unpushed/Unpulled
	unpushed, unpulled := getUnpushedUnpulled(workDir)
	if unpushed >= 0 {
		if unpushed > 0 {
			parts = append(parts, fmt.Sprintf("%sUnpushed: %d%s", BrightGreen, unpushed, Reset))
		} else {
			parts = append(parts, Dim+"Unpushed: 0"+Reset)
		}
	} else {
		parts = append(parts, Dim+"Unpushed: N/A"+Reset)
	}

	if unpulled >= 0 {
		if unpulled > 0 {
			parts = append(parts, fmt.Sprintf("%sUnpulled: %d%s", Blue, unpulled, Reset))
		} else {
			parts = append(parts, Dim+"Unpulled: 0"+Reset)
		}
	} else {
		parts = append(parts, Dim+"Unpulled: N/A"+Reset)
	}

	return strings.Join(parts, " "+Dim+"|"+Reset+" ")
}

// isGitRepo prüft ob das Verzeichnis ein Git-Repository ist
func isGitRepo(workDir string) bool {
	cmd := exec.Command("git", "-c", "core.useBuiltinFSMonitor=false", "rev-parse", "--git-dir")
	cmd.Dir = workDir
	err := cmd.Run()
	return err == nil
}

// gitCountLines führt einen Git-Befehl aus und zählt die Ausgabezeilen
func gitCountLines(workDir string, args ...string) int {
	fullArgs := append([]string{"-c", "core.useBuiltinFSMonitor=false", "--no-optional-locks"}, args...)
	cmd := exec.Command("git", fullArgs...)
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0
	}
	return len(lines)
}

// getUnpushedUnpulled holt die Anzahl der unpushed/unpulled Commits
func getUnpushedUnpulled(workDir string) (unpushed, unpulled int) {
	// Prüfe ob upstream existiert
	cmd := exec.Command("git", "-c", "core.useBuiltinFSMonitor=false", "rev-parse", "--abbrev-ref", "@{u}")
	cmd.Dir = workDir
	if err := cmd.Run(); err != nil {
		return -1, -1 // Kein upstream
	}

	// Unpushed
	cmd = exec.Command("git", "-c", "core.useBuiltinFSMonitor=false", "--no-optional-locks", "rev-list", "--count", "@{u}..HEAD")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		unpushed = -1
	} else {
		unpushed, _ = strconv.Atoi(strings.TrimSpace(string(output)))
	}

	// Unpulled
	cmd = exec.Command("git", "-c", "core.useBuiltinFSMonitor=false", "--no-optional-locks", "rev-list", "--count", "HEAD..@{u}")
	cmd.Dir = workDir
	output, err = cmd.Output()
	if err != nil {
		unpulled = -1
	} else {
		unpulled, _ = strconv.Atoi(strings.TrimSpace(string(output)))
	}

	return
}

// resolveWorkDir löst den Arbeitsverzeichnis-Pfad auf
func resolveWorkDir(cwd string) string {
	if cwd == "" || cwd == "~" {
		home, _ := os.UserHomeDir()
		return home
	}

	// Ersetze ~ mit Home-Verzeichnis
	if strings.HasPrefix(cwd, "~") {
		home, _ := os.UserHomeDir()
		cwd = filepath.Join(home, cwd[1:])
	}

	// Windows-Pfade normalisieren
	if runtime.GOOS == "windows" {
		cwd = strings.ReplaceAll(cwd, "/", "\\")
	}

	return cwd
}

// getSystemStats holt CPU und RAM Auslastung
func getSystemStats() (cpuPercent float64, memPercent float64) {
	// CPU Auslastung (kurzes Intervall für schnelle Antwort)
	cpuPercentages, err := cpu.Percent(100*time.Millisecond, false)
	if err == nil && len(cpuPercentages) > 0 {
		cpuPercent = cpuPercentages[0]
	}

	// RAM Auslastung
	memInfo, err := mem.VirtualMemory()
	if err == nil {
		memPercent = memInfo.UsedPercent
	}

	return
}

// getSessionDuration ermittelt die Session-Dauer anhand der CreateTime des CC-Prozesses
func getSessionDuration() time.Duration {
	ppid := int32(os.Getppid())
	proc, err := process.NewProcess(ppid)
	if err != nil {
		return 0
	}

	createTime, err := proc.CreateTime() // Millisekunden seit Epoch
	if err != nil {
		return 0
	}

	// Wenn Parent erst kürzlich erstellt (<2s), ist es ein Shell-Wrapper
	// → zum Grandparent (CC-Prozess) hochgehen
	if time.Now().UnixMilli()-createTime < 2000 {
		gppid, err := proc.Ppid()
		if err != nil {
			return time.Since(time.UnixMilli(createTime))
		}
		gproc, err := process.NewProcess(gppid)
		if err != nil {
			return time.Since(time.UnixMilli(createTime))
		}
		gCreateTime, err := gproc.CreateTime()
		if err != nil {
			return time.Since(time.UnixMilli(createTime))
		}
		return time.Since(time.UnixMilli(gCreateTime))
	}

	return time.Since(time.UnixMilli(createTime))
}

// formatSessionDuration formatiert die Session-Dauer kompakt
func formatSessionDuration(d time.Duration) string {
	d = d.Round(time.Minute)

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// renderMiniBar rendert eine kompakte Progress-Bar
func renderMiniBar(percent float64, width int) string {
	filled := int(percent * float64(width) / 100)
	if filled > width {
		filled = width
	}
	empty := width - filled

	color := getColorForPercentage(int(percent))
	return fmt.Sprintf("%s%s%s%s", color, strings.Repeat("█", filled), strings.Repeat("░", empty), Reset)
}

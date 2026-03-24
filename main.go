package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
	"golang.org/x/term"
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

	// Terminalbreite ermitteln
	termWidth := getTerminalWidth()
	if termWidth < 40 {
		// Minimal-Fallback bei extrem schmalen Terminals
		fmt.Println(BrightMagenta + Bold + modelName + Reset)
		fmt.Println(IconFolder + " " + Cyan + shortenPathTo(cwd, 30) + Reset)
		fmt.Println(IconSession + " " + Dim + "..." + Reset)
		return
	}

	// Externe Daten holen
	gitData := getGitData(resolveWorkDir(cwd))
	cpuPercent, memPercent := getSystemStats()
	sessionDur := getSessionDuration()

	// Worktree-Name extrahieren
	worktreeName := ""
	if input.Worktree != nil {
		worktreeName = input.Worktree.Name
	}

	// Kosten extrahieren
	costUSD := 0.0
	if input.Cost != nil {
		costUSD = input.Cost.TotalCostUSD
	}

	// 3 Zeilen rendern
	line1 := renderLine1(modelName, currentTokens, contextSize, usage, termWidth)
	line2 := renderLine2(cwd, gitData, worktreeName, termWidth)
	line3 := renderLine3(cpuPercent, memPercent, sessionDur, costUSD, termWidth)

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

// ansiRegex entfernt ANSI Escape-Sequences aus Strings
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// wideEmojis enthält die im Projekt verwendeten Emojis (2 Spalten breit)
var wideEmojis = map[rune]bool{
	'🧠': true, '💻': true, '🎛': true, '💰': true, '⏳': true,
	'🌿': true, '📂': true, '⏱': true, '📅': true,
}

// visibleWidth berechnet die sichtbare Breite eines ANSI-farbigen Strings
func visibleWidth(s string) int {
	clean := ansiRegex.ReplaceAllString(s, "")
	width := 0
	for _, r := range clean {
		if wideEmojis[r] {
			width += 2
		} else {
			width++
		}
	}
	return width
}

// truncateToWidth schneidet einen String auf maxWidth sichtbare Spalten ab
func truncateToWidth(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	var result strings.Builder
	width := 0
	i := 0
	bytes := []byte(s)

	for i < len(bytes) {
		// ANSI Escape-Sequence erkennen und komplett durchlassen
		if bytes[i] == '\x1b' && i+1 < len(bytes) && bytes[i+1] == '[' {
			start := i
			i += 2
			for i < len(bytes) && bytes[i] != 'm' {
				i++
			}
			if i < len(bytes) {
				i++ // 'm' überspringen
			}
			result.Write(bytes[start:i])
			continue
		}

		r, size := utf8.DecodeRune(bytes[i:])
		runeWidth := 1
		if wideEmojis[r] {
			runeWidth = 2
		}

		if width+runeWidth > maxWidth {
			break
		}
		result.WriteRune(r)
		width += runeWidth
		i += size
	}

	// Reset anhängen damit Farben nicht leaken
	result.WriteString(Reset)
	return result.String()
}

// shortenPathTo kürzt einen Pfad auf maxLen sichtbare Zeichen
func shortenPathTo(path string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	// Erst Home-Verzeichnis ersetzen
	path = shortenPath(path)

	if utf8.RuneCountInString(path) <= maxLen {
		return path
	}

	sep := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		sep = "\\"
	}
	// Auch Forward-Slashes akzeptieren (Unix-Pfade in shortenPath verwenden /)
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })

	if len(parts) <= 1 {
		// Nur ein Segment — direkt abschneiden
		if utf8.RuneCountInString(path) > maxLen {
			runes := []rune(path)
			if maxLen > 1 {
				return string(runes[:maxLen-1]) + "…"
			}
			return "…"
		}
		return path
	}

	// Prefix (~ oder Laufwerk)
	prefix := ""
	if strings.HasPrefix(path, "~") {
		prefix = "~" + sep
		parts = parts[1:] // ~ aus parts entfernen
	} else if runtime.GOOS == "windows" && len(parts) > 0 && len(parts[0]) == 2 && parts[0][1] == ':' {
		prefix = parts[0] + sep
		parts = parts[1:]
	}

	if len(parts) == 0 {
		return prefix
	}

	// Versuche schrittweise mittlere Segmente zu kürzen
	last := parts[len(parts)-1]

	// Minimale Darstellung: prefix + … + sep + last
	minimal := prefix + "…" + sep + last
	if utf8.RuneCountInString(minimal) > maxLen {
		// Selbst minimal zu lang — nur letztes Verzeichnis
		if utf8.RuneCountInString(last) > maxLen {
			runes := []rune(last)
			if maxLen > 1 {
				return string(runes[:maxLen-1]) + "…"
			}
			return "…"
		}
		return last
	}

	// Von außen nach innen: erstes + letztes beibehalten, Mitte kürzen
	for keep := len(parts) - 1; keep >= 1; keep-- {
		// Behalte erste 'keep-1' Segmente und letztes Segment
		kept := parts[:keep-1]
		candidate := prefix + strings.Join(kept, sep) + sep + "…" + sep + last
		if keep == 1 {
			candidate = prefix + "…" + sep + last
		}
		if utf8.RuneCountInString(candidate) <= maxLen {
			// Versuche mehr Segmente einzubauen
			best := candidate
			for j := keep; j < len(parts)-1; j++ {
				trial := prefix + strings.Join(parts[:j], sep) + sep + "…" + sep + last
				if utf8.RuneCountInString(trial) <= maxLen {
					best = trial
				} else {
					break
				}
			}
			return best
		}
	}

	return minimal
}

// getTerminalWidth ermittelt die Terminalbreite
func getTerminalWidth() int {
	// 1. COLUMNS env-Variable (höchste Priorität, z.B. für Tests)
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if w, err := strconv.Atoi(cols); err == nil && w > 0 {
			return w
		}
	}

	// 2. Plattform-spezifisch Console-Handle öffnen (funktioniert auch bei gepipted stdout)
	if runtime.GOOS == "windows" {
		if f, err := os.Open("CONOUT$"); err == nil {
			defer f.Close()
			if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
				return w
			}
		}
	} else {
		if f, err := os.Open("/dev/tty"); err == nil {
			defer f.Close()
			if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
				return w
			}
		}
	}

	// 3. Fallback
	return 120
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

// --- Zeilen-Layout-Funktionen ---

// makeSep erzeugt den Standard-Separator
func makeSep() string {
	return "  " + Dim + "•" + Reset + "  "
}

const sepVisibleWidth = 5 // sichtbare Breite von "  •  "

// renderLine1 rendert Zeile 1: Model + Context + Rate Limits
func renderLine1(modelName string, currentTokens, contextSize int, usage UsageData, termWidth int) string {
	showBars := termWidth >= 100
	sep := makeSep()

	show7Day := termWidth >= 80 && usage.SevenDayUtil >= 0

	// Separator-Budget
	numSeps := 2 // Model•Context•5h
	if show7Day {
		numSeps = 3
	}

	// Model bekommt was es braucht
	modelStr := BrightMagenta + Bold + modelName + Reset
	modelWidth := visibleWidth(modelStr)

	available := termWidth - (numSeps * sepVisibleWidth) - modelWidth

	// Budget aufteilen
	var ctxBudget, fiveHBudget, sevenDBudget int
	if show7Day {
		ctxBudget = available * 40 / 100
		fiveHBudget = available * 30 / 100
		sevenDBudget = available * 20 / 100
		// Rundungsrest an Context
		ctxBudget += available - ctxBudget - fiveHBudget - sevenDBudget
	} else {
		ctxBudget = available * 55 / 100
		fiveHBudget = available * 40 / 100
		// Rundungsrest an Context
		ctxBudget += available - ctxBudget - fiveHBudget
	}

	parts := []string{modelStr}
	parts = append(parts, renderContextElement(currentTokens, contextSize, ctxBudget, showBars))

	if usage.FiveHourUtil >= 0 {
		parts = append(parts, renderRateLimitElement(usage.FiveHourUtil, usage.FiveHourReset, "5h", IconClock, fiveHBudget, showBars, BrightMagenta))
	} else {
		parts = append(parts, Dim+IconClock+" 5h: N/A"+Reset)
	}

	if show7Day {
		parts = append(parts, renderRateLimitElement(usage.SevenDayUtil, usage.SevenDayReset, "7d", IconCalendar, sevenDBudget, showBars, BrightYellow))
	}

	line := strings.Join(parts, sep)
	return truncateToWidth(line, termWidth)
}

// renderLine2 rendert Zeile 2: CWD + Git + Worktree
func renderLine2(cwd string, gitData GitData, worktreeName string, termWidth int) string {
	sep := makeSep()

	showWorktree := termWidth >= 100 && worktreeName != ""

	numSeps := 1 // CWD•Git
	if showWorktree {
		numSeps = 2
	}

	available := termWidth - (numSeps * sepVisibleWidth)

	// Budget aufteilen
	var cwdBudget, gitBudget, worktreeBudget int
	if showWorktree {
		cwdBudget = available * 30 / 100
		gitBudget = available * 50 / 100
		worktreeBudget = available * 20 / 100
		cwdBudget += available - cwdBudget - gitBudget - worktreeBudget
	} else {
		cwdBudget = available * 35 / 100
		gitBudget = available * 65 / 100
		cwdBudget += available - cwdBudget - gitBudget
	}

	// CWD rendern (Icon 📂 = 2 Spalten + Space = 3)
	cwdPathBudget := cwdBudget - 3
	if cwdPathBudget < 5 {
		cwdPathBudget = 5
	}
	shortenedCwd := shortenPathTo(cwd, cwdPathBudget)
	cwdStr := fmt.Sprintf("%s %s%s%s", IconFolder, Cyan, shortenedCwd, Reset)

	// Git rendern (Icon ⎇ = 1 Spalte + Space = 2)
	gitBudgetForContent := gitBudget - 2
	if gitBudgetForContent < 5 {
		gitBudgetForContent = 5
	}
	gitStr := fmt.Sprintf("%s %s", IconGit, renderGitElement(gitData, gitBudgetForContent))

	parts := []string{cwdStr, gitStr}

	if showWorktree {
		wtStr := fmt.Sprintf("%s %s%s%s", IconWorktree, BrightGreen, worktreeName, Reset)
		parts = append(parts, wtStr)
	}

	line := strings.Join(parts, sep)
	return truncateToWidth(line, termWidth)
}

// renderLine3 rendert Zeile 3: System Stats + Session + Cost
func renderLine3(cpuPct, memPct float64, sessionDur time.Duration, costUSD float64, termWidth int) string {
	showBars := termWidth >= 100
	sep := makeSep()

	// Elemente sammeln
	type lineElement struct {
		str string
	}
	var parts []lineElement

	if termWidth >= 70 {
		// CPU und RAM separat
		cpuBudget := 15
		ramBudget := 15
		if termWidth < 100 {
			cpuBudget = 8
			ramBudget = 8
		}
		parts = append(parts, lineElement{renderSystemElement(IconCPU, "CPU", cpuPct, cpuBudget, showBars)})
		parts = append(parts, lineElement{renderSystemElement(IconRAM, "RAM", memPct, ramBudget, showBars)})
	} else {
		// CPU/RAM kompakt zusammen
		cpuColor := getColorForPercentage(int(cpuPct))
		ramColor := getColorForPercentage(int(memPct))
		compact := fmt.Sprintf("C%s%.0f%%%s R%s%.0f%%%s",
			cpuColor, cpuPct, Reset,
			ramColor, memPct, Reset,
		)
		parts = append(parts, lineElement{compact})
	}

	// Session
	if sessionDur > 0 {
		parts = append(parts, lineElement{
			fmt.Sprintf("%s %s%s%s", IconSession, BrightCyan, formatSessionDuration(sessionDur), Reset),
		})
	} else {
		parts = append(parts, lineElement{
			fmt.Sprintf("%s %s<1m%s", IconSession, Dim, Reset),
		})
	}

	// Cost
	if costUSD > 0 {
		parts = append(parts, lineElement{
			fmt.Sprintf("%s %s$%.2f%s", IconCost, BrightYellow, costUSD, Reset),
		})
	}

	strs := make([]string, len(parts))
	for i, p := range parts {
		strs[i] = p.str
	}
	line := strings.Join(strs, sep)
	return truncateToWidth(line, termWidth)
}

// --- Adaptive Render-Funktionen ---

// renderModelElement rendert den Model-Namen adaptiv
func renderModelElement(name string, budget int) string {
	styled := BrightMagenta + Bold + name + Reset
	if visibleWidth(styled) <= budget {
		return styled
	}
	// Abkürzen wenn zu wenig Platz
	runes := []rune(name)
	for len(runes) > 1 && visibleWidth(BrightMagenta+Bold+string(runes)+Reset) > budget {
		runes = runes[:len(runes)-1]
	}
	return BrightMagenta + Bold + string(runes) + Reset
}

// renderContextElement rendert die Context-Anzeige adaptiv
func renderContextElement(current, max, budget int, showBars bool) string {
	if max == 0 {
		return Dim + IconBrain + " Ctx: N/A" + Reset
	}

	percentage := current * 100 / max
	color := getColorForPercentage(percentage)
	currentFmt := formatWithKSuffix(current)
	maxFmt := formatWithKSuffix(max)

	if showBars && budget >= 24 {
		// Volle Bar-Darstellung: "🧠 Ctx: 52k/200k ████░░ 26%"
		// 🧠(2) + " Ctx: "(6) + values(~9) + " "(1) + bar + " "(1) + pct(~3) ≈ 22 + barWidth
		barWidth := budget - 22
		if barWidth < 3 {
			barWidth = 3
		}
		if barWidth > 20 {
			barWidth = 20
		}
		return renderProgressBarWithValues(current, max, IconBrain+" Ctx", BrightCyan, barWidth)
	}

	if budget >= 20 {
		// Kompakt mit Werten: "🧠 Ctx: 52k/200k 26%" ≈ 20 sichtbare Zeichen
		return fmt.Sprintf("%s%s Ctx:%s %s/%s %s%d%%%s",
			BrightCyan, IconBrain, Reset,
			currentFmt, maxFmt,
			color, percentage, Reset,
		)
	}

	// Minimal: "🧠 Ctx: 26%" ≈ 11 sichtbare Zeichen
	return fmt.Sprintf("%s%s Ctx:%s %s%d%%%s",
		BrightCyan, IconBrain, Reset,
		color, percentage, Reset,
	)
}

// renderRateLimitElement rendert ein Rate-Limit-Element adaptiv
func renderRateLimitElement(pct int, resetTs int64, label, icon string, budget int, showBars bool, labelColor string) string {
	if pct < 0 {
		return Dim + icon + " " + label + ": N/A" + Reset
	}

	color := getColorForPercentage(pct)

	if showBars && budget >= 20 {
		// Bar + optional Reset-Zeit: ⏱(2) + " 5h:"(4) + " "(1) + bar + " "(1) + pct(~3) ≈ 11 + barWidth
		barWidth := budget - 14
		if barWidth < 3 {
			barWidth = 3
		}
		if barWidth > 15 {
			barWidth = 15
		}
		result := renderProgressBar(pct, 100, icon+" "+label, barWidth, labelColor)
		// Reset-Zeit anhängen wenn Platz
		if resetTs > 0 {
			resetTime := time.Unix(resetTs, 0)
			timeUntil := time.Until(resetTime)
			if timeUntil > 0 {
				resetStr := fmt.Sprintf(" %sin %s (%s)%s", Dim, formatDuration(timeUntil), resetTime.Local().Format("15:04"), Reset)
				if visibleWidth(result)+visibleWidth(resetStr) <= budget {
					result += resetStr
				} else {
					// Nur Uhrzeit wenn Platz
					shortReset := fmt.Sprintf(" %s%s%s", Dim, resetTime.Local().Format("15:04"), Reset)
					if visibleWidth(result)+visibleWidth(shortReset) <= budget {
						result += shortReset
					}
				}
			}
		}
		return result
	}

	if budget >= 10 {
		// Kompakt: "⏱ 5h: 45% 12:30" ≈ 10 + optional 6 für Zeit
		result := fmt.Sprintf("%s%s %s:%s %s%d%%%s",
			labelColor, icon, label, Reset,
			color, pct, Reset,
		)
		if resetTs > 0 {
			resetTime := time.Unix(resetTs, 0)
			timeStr := fmt.Sprintf(" %s%s%s", Dim, resetTime.Local().Format("15:04"), Reset)
			if visibleWidth(result)+visibleWidth(timeStr) <= budget {
				result += timeStr
			}
		}
		return result
	}

	// Minimal: "⏱ 5h: 45%"
	return fmt.Sprintf("%s%s %s:%s %s%d%%%s",
		labelColor, icon, label, Reset,
		color, pct, Reset,
	)
}

// renderGitElement rendert den Git-Status adaptiv
func renderGitElement(data GitData, budget int) string {
	if !data.IsRepo {
		return Dim + "N/A" + Reset
	}

	sep := " " + Dim + "|" + Reset + " "
	sepWidth := 3 // sichtbare Breite von " | "

	// Alle möglichen Parts mit ihrer sichtbaren Breite vorbereiten
	type part struct {
		str      string
		priority int // 1=höchste
	}

	var allParts []part

	// Änderungen (Prio 1)
	if data.Changes > 0 {
		allParts = append(allParts, part{fmt.Sprintf("%sÄnd: %d%s", Yellow, data.Changes, Reset), 1})
	} else {
		allParts = append(allParts, part{Dim + "Änd: 0" + Reset, 1})
	}

	// Staged (Prio 2)
	if data.Staged > 0 {
		allParts = append(allParts, part{fmt.Sprintf("%sStg: %d%s", BrightGreen, data.Staged, Reset), 2})
	} else {
		allParts = append(allParts, part{Dim + "Stg: 0" + Reset, 2})
	}

	// Stash (Prio 4)
	if data.Stash > 0 {
		allParts = append(allParts, part{fmt.Sprintf("%sStash: %d%s", Cyan, data.Stash, Reset), 4})
	}

	// Unpushed (Prio 2)
	if data.HasUpstream {
		if data.Unpushed > 0 {
			allParts = append(allParts, part{fmt.Sprintf("%s↑%d%s", BrightGreen, data.Unpushed, Reset), 2})
		} else {
			allParts = append(allParts, part{Dim + "↑0" + Reset, 3})
		}
	}

	// Unpulled (Prio 3)
	if data.HasUpstream {
		if data.Unpulled > 0 {
			allParts = append(allParts, part{fmt.Sprintf("%s↓%d%s", Blue, data.Unpulled, Reset), 3})
		} else {
			allParts = append(allParts, part{Dim + "↓0" + Reset, 4})
		}
	}

	// Parts nach Budget filtern — von hinten (niedrigste Prio) entfernen
	for len(allParts) > 1 {
		totalWidth := 0
		for i, p := range allParts {
			totalWidth += visibleWidth(p.str)
			if i > 0 {
				totalWidth += sepWidth
			}
		}
		if totalWidth <= budget {
			break
		}
		// Entferne Part mit höchster Prio-Zahl (niedrigste Priorität)
		worstIdx := 0
		worstPrio := 0
		for i, p := range allParts {
			if p.priority > worstPrio {
				worstPrio = p.priority
				worstIdx = i
			}
		}
		allParts = append(allParts[:worstIdx], allParts[worstIdx+1:]...)
	}

	// Parts zusammenfügen
	strs := make([]string, len(allParts))
	for i, p := range allParts {
		strs[i] = p.str
	}
	return strings.Join(strs, sep)
}

// renderSystemElement rendert CPU oder RAM adaptiv
func renderSystemElement(icon, label string, pct float64, budget int, showBars bool) string {
	color := getColorForPercentage(int(pct))

	if showBars && budget >= 12 {
		barWidth := budget - 8
		if barWidth < 3 {
			barWidth = 3
		}
		if barWidth > 10 {
			barWidth = 10
		}
		return fmt.Sprintf("%s %s: %s %s%.0f%%%s",
			icon, label, renderMiniBar(pct, barWidth),
			color, pct, Reset,
		)
	}

	if budget >= 6 {
		return fmt.Sprintf("%s %s%.0f%%%s", label, color, pct, Reset)
	}

	// Minimal: "C52%"
	short := label
	if len(short) > 1 {
		short = string([]rune(short)[0:1])
	}
	return fmt.Sprintf("%s%s%.0f%%%s", short, color, pct, Reset)
}

// GitData enthält die rohen Git-Statusdaten (ohne Rendering)
type GitData struct {
	Changes     int
	Staged      int
	Stash       int
	Unpushed    int  // -1 = kein Upstream
	Unpulled    int  // -1 = kein Upstream
	IsRepo      bool
	HasUpstream bool
}

// getGitData holt die Git-Statusdaten (nur I/O, kein Rendering)
func getGitData(workDir string) GitData {
	if !isGitRepo(workDir) {
		return GitData{IsRepo: false}
	}

	data := GitData{IsRepo: true}
	data.Changes = gitCountLines(workDir, "status", "--porcelain")
	data.Staged = gitCountLines(workDir, "diff", "--cached", "--numstat")
	data.Stash = gitCountLines(workDir, "stash", "list")
	data.Unpushed, data.Unpulled = getUnpushedUnpulled(workDir)
	data.HasUpstream = data.Unpushed >= 0

	return data
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

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
)

// API Konfiguration
const (
	APIEndpoint   = "https://api.anthropic.com/api/oauth/usage"
	AnthropicBeta = "oauth-2025-04-20"
	UserAgent     = "claude-code/2.1.70"
	CacheDuration = 60 // Sekunden
)

// Input JSON Struktur (von Claude Code)
type StatusLineInput struct {
	ContextWindow struct {
		CurrentUsage *struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"current_usage"`
		ContextWindowSize int `json:"context_window_size"`
	} `json:"context_window"`
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	Cwd string `json:"cwd"`
}

// Credentials JSON Struktur
type Credentials struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

// Usage API Response
type UsageResponse struct {
	FiveHour struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"five_hour"`
	SevenDay struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"seven_day"`
}

// UsageData gecachte Daten
type UsageData struct {
	FiveHourUtil   int
	SevenDayUtil   int
	FiveHourReset  string
	SevenDayReset  string
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

	// Context Window berechnen
	var currentTokens int
	contextSize := input.ContextWindow.ContextWindowSize
	if contextSize == 0 {
		contextSize = 200000
	}
	if input.ContextWindow.CurrentUsage != nil {
		cu := input.ContextWindow.CurrentUsage
		currentTokens = cu.InputTokens + cu.CacheCreationInputTokens + cu.CacheReadInputTokens
	}

	// Usage Limits holen (mit Caching)
	usage := getUsageLimits()

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
		if usage.FiveHourReset != "" && usage.FiveHourReset != "N/A" {
			resetTime, _ := time.Parse(time.RFC3339, usage.FiveHourReset)
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
		if usage.SevenDayReset != "" && usage.SevenDayReset != "N/A" {
			resetTime, _ := time.Parse(time.RFC3339, usage.SevenDayReset)
			line1 += fmt.Sprintf(" %s%s%s", Dim, resetTime.Local().Format("02. Jan 15:04"), Reset)
		}
	} else {
		line1 += Dim + IconCalendar + " 7d: N/A" + Reset
	}

	// System Stats holen
	cpuPercent, memPercent := getSystemStats()
	sessionDur := getSessionDuration()

	// Zeile 2: CWD + Git Status
	line2 := fmt.Sprintf("%s %s%s%s  %s•%s  %s %s",
		IconFolder, Cyan, cwd, Reset,
		Dim, Reset,
		IconGit, gitStatus,
	)

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

	fmt.Println(line1)
	fmt.Println(line2)
	fmt.Println(line3)
}

// getUsageLimits holt Usage-Daten von der API (mit Caching)
func getUsageLimits() UsageData {
	// Off-Switch: STATUSLINE_DISABLE_USAGE=1 deaktiviert den API-Aufruf komplett
	if os.Getenv("STATUSLINE_DISABLE_USAGE") == "1" {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}
	}

	cacheFile := filepath.Join(getClaudeDir(), "cache", "usage_persist_cache.txt")

	// Cache prüfen
	if cached, ok := readCache(cacheFile); ok {
		return cached
	}

	// API aufrufen
	usage := fetchUsageFromAPI()

	// Nur erfolgreiche Responses cachen (nicht -1/Fehler)
	if usage.FiveHourUtil >= 0 || usage.SevenDayUtil >= 0 {
		saveCache(cacheFile, usage)
	}

	return usage
}

// readCache liest gecachte Usage-Daten
func readCache(cacheFile string) (UsageData, bool) {
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}, false
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}, false
	}

	timestamp, err := strconv.ParseInt(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}, false
	}

	// Cache zu alt?
	if time.Now().Unix()-timestamp > CacheDuration {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}, false
	}

	// Parse cached data: util:100:util:100|reset_5h|reset_weekly
	cached := strings.TrimSpace(lines[1])
	parts := strings.Split(cached, "|")
	if len(parts) < 3 {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}, false
	}

	numbers := strings.Split(parts[0], ":")
	if len(numbers) < 4 {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}, false
	}

	fiveHour, _ := strconv.Atoi(numbers[0])
	sevenDay, _ := strconv.Atoi(numbers[2])

	return UsageData{
		FiveHourUtil:  fiveHour,
		SevenDayUtil:  sevenDay,
		FiveHourReset: parts[1],
		SevenDayReset: parts[2],
	}, true
}

// saveCache speichert Usage-Daten im Cache
func saveCache(cacheFile string, usage UsageData) {
	// Sicherstellen dass das Verzeichnis existiert
	os.MkdirAll(filepath.Dir(cacheFile), 0755)

	content := fmt.Sprintf("%d\n%d:100:%d:100|%s|%s",
		time.Now().Unix(),
		usage.FiveHourUtil, usage.SevenDayUtil,
		usage.FiveHourReset, usage.SevenDayReset,
	)

	os.WriteFile(cacheFile, []byte(content), 0644)
}

// fetchUsageFromAPI holt Usage-Daten von der Anthropic API
func fetchUsageFromAPI() UsageData {
	token := getAccessToken()
	if token == "" {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}
	}

	client := &http.Client{Timeout: 5 * time.Second}

	req, err := http.NewRequest("GET", APIEndpoint, nil)
	if err != nil {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", AnthropicBeta)

	resp, err := client.Do(req)
	if err != nil {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}
	}

	var usageResp UsageResponse
	if err := json.Unmarshal(body, &usageResp); err != nil {
		return UsageData{FiveHourUtil: -1, SevenDayUtil: -1}
	}

	return UsageData{
		FiveHourUtil:  int(usageResp.FiveHour.Utilization),
		SevenDayUtil:  int(usageResp.SevenDay.Utilization),
		FiveHourReset: usageResp.FiveHour.ResetsAt,
		SevenDayReset: usageResp.SevenDay.ResetsAt,
	}
}

// getAccessToken liest den Access Token aus den Credentials
func getAccessToken() string {
	credsFile := filepath.Join(getClaudeDir(), ".credentials.json")

	data, err := os.ReadFile(credsFile)
	if err != nil {
		return ""
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return ""
	}

	return creds.ClaudeAiOauth.AccessToken
}

// getClaudeDir gibt das Claude-Konfigurationsverzeichnis zurück
func getClaudeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
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

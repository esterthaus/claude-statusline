package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	IconAIU      = "⚡"
	IconCache    = "🧊"
)

// Input JSON Struktur (von Claude Code bzw. Copilot CLI)
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
		// Copilot CLI: Display-Sicht (das, was /context anzeigt).
		// used_percentage bezieht sich dort nur auf den letzten Call.
		CurrentContextTokens  *int `json:"current_context_tokens"`
		DisplayedContextLimit *int `json:"displayed_context_limit"`
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
	// Copilot CLI: AIU-Verbrauch, ersetzt die Rate-Limit-Anzeige
	AIUsed *struct {
		TotalNanoAIU int64  `json:"total_nano_aiu"`
		Formatted    string `json:"formatted"`
	} `json:"ai_used"`
	// Copilot CLI: nur als Erkennungsmerkmal, der Inhalt wird nicht ausgewertet
	Remote            *json.RawMessage `json:"remote"`
	Version           string           `json:"version"`
	TranscriptPath    string           `json:"transcript_path"`
	SessionID         string           `json:"session_id"`
	Cwd               string           `json:"cwd"`
	Exceeds200kTokens bool             `json:"exceeds_200k_tokens"`
}

// ScopedLimit ist ein modellspezifisches Wochenlimit aus dem OAuth-Usage-Endpoint
type ScopedLimit struct {
	Name  string // scope.model.display_name, z.B. "Fable"
	Util  int    // Prozent
	Reset int64  // Unix-Sekunden, 0 = unbekannt
}

// UsageData fuer Rate-Limit-Anzeige
type UsageData struct {
	FiveHourUtil  int
	SevenDayUtil  int
	FiveHourReset int64
	SevenDayReset int64
	Scoped        []ScopedLimit // per Netz nachgeladen, nicht im stdin-JSON (0..n)
}

// CacheInfo beschreibt den Prompt-Cache der laufenden Konversation
type CacheInfo struct {
	ExpiresAt time.Time // Ablauf der Cache-TTL, Nullwert = unbekannt
	HitPct    int       // Trefferquote des letzten API-Calls, -1 = unbekannt
}

// Flavor unterscheidet die CLI, die das JSON geliefert hat
type Flavor int

const (
	FlavorClaude Flavor = iota
	FlavorCopilot
)

// Statusline enthält die normalisierten Anzeigedaten beider JSON-Dialekte
type Statusline struct {
	Flavor        Flavor
	ModelName     string
	Cwd           string
	CurrentTokens int
	ContextSize   int

	// Nur Claude Code
	Cache        CacheInfo
	Usage        UsageData
	WorktreeName string
	CostUSD      float64

	// Nur Copilot CLI
	AIU          string
	LinesAdded   int
	LinesRemoved int
	APIDuration  time.Duration
	SessionDur   time.Duration // 0 = auf Prozess-Heuristik zurückfallen
}

// detectFlavor erkennt Copilot CLI an Feldern, die Claude Code nicht liefert
func detectFlavor(in StatusLineInput) Flavor {
	if in.Remote != nil || in.AIUsed != nil || in.ContextWindow.CurrentContextTokens != nil {
		return FlavorCopilot
	}
	return FlavorClaude
}

// normalize überführt das Eingabe-JSON in die flavor-unabhängige Anzeigestruktur
func normalize(in StatusLineInput) Statusline {
	s := Statusline{Flavor: detectFlavor(in), Cache: CacheInfo{HitPct: -1}}

	s.ModelName = in.Model.DisplayName
	if s.ModelName == "" {
		s.ModelName = "Unknown"
	}

	cwd := in.Workspace.CurrentDir
	if cwd == "" {
		cwd = in.Cwd
	}
	if cwd == "" {
		cwd = "~"
	}
	s.Cwd = shortenPath(cwd)

	if s.Flavor == FlavorCopilot {
		s.CurrentTokens, s.ContextSize = copilotContext(in)
		if in.AIUsed != nil {
			s.AIU = in.AIUsed.Formatted
		}
		if in.Cost != nil {
			s.LinesAdded = in.Cost.TotalLinesAdded
			s.LinesRemoved = in.Cost.TotalLinesRemoved
			s.APIDuration = time.Duration(in.Cost.TotalAPIDurationMs) * time.Millisecond
			// Copilot liefert die Session-Dauer direkt (Date.now() - sessionStartTime)
			s.SessionDur = time.Duration(in.Cost.TotalDurationMs) * time.Millisecond
		}
		return s
	}

	s.CurrentTokens, s.ContextSize = claudeContext(in)
	s.Cache.HitPct = cacheHitPct(in)
	s.Usage = extractRateLimits(in)
	if in.Worktree != nil {
		s.WorktreeName = in.Worktree.Name
	}
	if in.Cost != nil {
		s.CostUSD = in.Cost.TotalCostUSD
	}
	return s
}

// claudeContext nutzt die native used_percentage, Fallback auf manuelle Berechnung
func claudeContext(in StatusLineInput) (tokens, size int) {
	cw := in.ContextWindow
	size = cw.ContextWindowSize
	if size == 0 {
		size = 200000
	}

	if cw.UsedPercentage != nil {
		tokens = int(*cw.UsedPercentage) * size / 100
	} else if cw.CurrentUsage != nil {
		tokens = cw.CurrentUsage.InputTokens + cw.CurrentUsage.CacheCreationInputTokens + cw.CurrentUsage.CacheReadInputTokens
	}
	return
}

// copilotContext nutzt die Display-Sicht; ohne Limit bleibt size 0 → "Ctx: N/A"
func copilotContext(in StatusLineInput) (tokens, size int) {
	cw := in.ContextWindow
	if cw.CurrentContextTokens != nil {
		tokens = *cw.CurrentContextTokens
	}
	if cw.DisplayedContextLimit != nil && *cw.DisplayedContextLimit > 0 {
		size = *cw.DisplayedContextLimit
	} else {
		size = cw.ContextWindowSize
	}
	return
}

// Prompt-Cache: das Transcript wird nur am Ende gelesen, die Statuszeile laeuft im Sekundentakt
const transcriptTailBytes = 256 * 1024

// transcriptEntry ist der Ausschnitt einer Transcript-Zeile, den die Cache-Berechnung braucht
type transcriptEntry struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	Message     struct {
		Usage *struct {
			CacheCreation *struct {
				Ephemeral5m int `json:"ephemeral_5m_input_tokens"`
				Ephemeral1h int `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// cacheHitPct ist der Anteil der Prompt-Tokens des letzten API-Calls, der aus dem Cache kam.
// Faellt nach einer Kompaktierung oder abgelaufenem Cache sofort ab. -1 = unbekannt.
func cacheHitPct(in StatusLineInput) int {
	cu := in.ContextWindow.CurrentUsage
	if cu == nil {
		return -1
	}
	total := cu.InputTokens + cu.CacheCreationInputTokens + cu.CacheReadInputTokens
	if total <= 0 {
		return -1
	}
	return cu.CacheReadInputTokens * 100 / total
}

// tailLines liest die letzten max Bytes einer Datei als Zeilen. Die erste Zeile wird verworfen,
// wenn nicht von Dateianfang an gelesen wurde - sie ist dann angeschnitten.
func tailLines(path string, max int64) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var off int64
	if st.Size() > max {
		off = st.Size() - max
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(data, []byte("\n"))
	if off > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	return lines, nil
}

// readCacheExpiry bestimmt, wie lange der Prompt-Cache der Konversation noch warm ist.
// Zeitanker ist der letzte API-Call der Hauptkonversation (Sidechains der Subagenten haben
// eigene Caches und zaehlen nicht), denn jeder Cache-Treffer erneuert die TTL. Die TTL selbst
// steht in cache_creation des juengsten Calls, der ueberhaupt etwas geschrieben hat:
// ephemeral_1h_input_tokens > 0 bedeutet 1 h, ephemeral_5m_input_tokens > 0 bedeutet 5 min.
// Ohne ermittelbare TTL bleibt der Rueckgabewert leer - lieber kein Element als eine geratene Zahl.
// note beschreibt das Ergebnis fuers Debug-Log.
func readCacheExpiry(path string) (time.Time, string) {
	if path == "" {
		return time.Time{}, "no path"
	}
	lines, err := tailLines(path, transcriptTailBytes)
	if err != nil {
		return time.Time{}, "err: " + err.Error()
	}

	var last time.Time
	for i := len(lines) - 1; i >= 0; i-- {
		var e transcriptEntry
		if json.Unmarshal(lines[i], &e) != nil {
			continue
		}
		if e.Type != "assistant" || e.IsSidechain || e.Message.Usage == nil {
			continue
		}
		if last.IsZero() {
			ts, terr := time.Parse(time.RFC3339, e.Timestamp)
			if terr != nil {
				continue
			}
			last = ts
		}
		cc := e.Message.Usage.CacheCreation
		if cc == nil {
			continue
		}
		if cc.Ephemeral1h > 0 {
			return last.Add(time.Hour), "1h"
		}
		if cc.Ephemeral5m > 0 {
			return last.Add(5 * time.Minute), "5m"
		}
	}
	return time.Time{}, "no ttl"
}

// extractRateLimits liest die Rate Limits, -1 markiert fehlende Werte
func extractRateLimits(in StatusLineInput) UsageData {
	usage := UsageData{FiveHourUtil: -1, SevenDayUtil: -1}
	if in.RateLimits == nil {
		return usage
	}
	if in.RateLimits.FiveHour != nil {
		usage.FiveHourUtil = int(in.RateLimits.FiveHour.UsedPercentage)
		usage.FiveHourReset = int64(in.RateLimits.FiveHour.ResetsAt)
	}
	if in.RateLimits.SevenDay != nil {
		usage.SevenDayUtil = int(in.RateLimits.SevenDay.UsedPercentage)
		usage.SevenDayReset = int64(in.RateLimits.SevenDay.ResetsAt)
	}
	return usage
}

// --- Modellspezifische Wochenlimits (OAuth-Usage-Endpoint) ---

const (
	usageEndpoint     = "https://api.anthropic.com/api/oauth/usage"
	usageCacheFile    = "statusline-usage-cache.json" // in os.TempDir()
	usageCacheTTL     = 60 * time.Second              // Mindestabstand zwischen Requests
	usageCacheStale   = 15 * time.Minute              // max. Alter, bis zu dem alte Daten noch angezeigt werden
	usageFetchTimeout = 3 * time.Second
)

// usageCache speichert die rohe Antwort des Usage-Endpoints. checked_at bremst auch nach
// Fehlern (429, offline), fetched_at begrenzt, wie lange veraltete Daten noch angezeigt werden.
type usageCache struct {
	FetchedAt int64           `json:"fetched_at"`
	CheckedAt int64           `json:"checked_at"`
	Body      json.RawMessage `json:"body"`
}

func usageCachePath() string { return filepath.Join(os.TempDir(), usageCacheFile) }

func loadUsageCache() (usageCache, bool) {
	var c usageCache
	data, err := os.ReadFile(usageCachePath())
	if err != nil || json.Unmarshal(data, &c) != nil {
		return usageCache{}, false
	}
	return c, true
}

// saveUsageCache schreibt atomar (Temp-Datei + Rename), damit parallele Aufrufe nie eine halbe Datei lesen
func saveUsageCache(c usageCache) {
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	f, err := os.CreateTemp(os.TempDir(), "statusline-usage-cache-*.tmp")
	if err != nil {
		return
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil || os.Rename(f.Name(), usageCachePath()) != nil {
		os.Remove(f.Name())
	}
}

// readOAuthToken liest das Access-Token aus ~/.claude/.credentials.json. Der Refresh-Token wird
// bewusst nie benutzt: Claude Code rotiert ihn selbst, ein Fremd-Refresh würde die Session stören.
func readOAuthToken() (string, error) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".claude")
	}
	data, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		return "", err
	}
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"` // Millisekunden
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", err
	}
	tok := creds.ClaudeAiOauth.AccessToken
	if tok == "" {
		return "", fmt.Errorf("kein OAuth-Token")
	}
	if exp := creds.ClaudeAiOauth.ExpiresAt; exp > 0 && time.Now().UnixMilli() >= exp {
		return "", fmt.Errorf("OAuth-Token abgelaufen")
	}
	return tok, nil
}

// parseScopedLimits filtert aus limits[] die modellspezifischen Wochenlimits (kind == weekly_scoped)
func parseScopedLimits(body []byte) []ScopedLimit {
	var r struct {
		Limits []struct {
			Kind     string  `json:"kind"`
			Percent  float64 `json:"percent"`
			ResetsAt string  `json:"resets_at"`
			Scope    *struct {
				Model *struct {
					DisplayName string `json:"display_name"`
				} `json:"model"`
			} `json:"scope"`
		} `json:"limits"`
	}
	if json.Unmarshal(body, &r) != nil {
		return nil
	}
	var limits []ScopedLimit
	for _, l := range r.Limits {
		if l.Kind != "weekly_scoped" || l.Scope == nil || l.Scope.Model == nil || l.Scope.Model.DisplayName == "" {
			continue
		}
		sl := ScopedLimit{Name: l.Scope.Model.DisplayName, Util: int(l.Percent)}
		if t, err := time.Parse(time.RFC3339, l.ResetsAt); err == nil {
			sl.Reset = t.Unix()
		}
		limits = append(limits, sl)
	}
	return limits
}

// fetchUsage holt die rohe Antwort des Usage-Endpoints
func fetchUsage(token string) ([]byte, error) {
	req, err := http.NewRequest("GET", usageEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := (&http.Client{Timeout: usageFetchTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return body, nil
}

// fetchScopedLimits liefert die modellspezifischen Wochenlimits (z.B. "Fable"), die Claude Code
// nicht im Statusline-JSON mitgibt. Cache in os.TempDir(), nie stdout, Fehler → leere Liste.
// note beschreibt die Quelle fürs Debug-Log: "cache", "network", "stale: …", "none: …".
func fetchScopedLimits() (limits []ScopedLimit, note string) {
	now := time.Now()
	c, ok := loadUsageCache()
	if ok && now.Sub(time.Unix(c.CheckedAt, 0)) < usageCacheTTL {
		return parseScopedLimits(c.Body), "cache"
	}

	var body []byte
	token, err := readOAuthToken()
	if err == nil {
		body, err = fetchUsage(token)
	}
	if err == nil {
		saveUsageCache(usageCache{FetchedAt: now.Unix(), CheckedAt: now.Unix(), Body: body})
		return parseScopedLimits(body), "network"
	}

	// Fehler: Versuch merken (Backoff für eine TTL), ggf. veraltete Daten weiterverwenden
	c.CheckedAt = now.Unix()
	saveUsageCache(c)
	if now.Sub(time.Unix(c.FetchedAt, 0)) < usageCacheStale {
		return parseScopedLimits(c.Body), "stale: " + err.Error()
	}
	return nil, "none: " + err.Error()
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

	// Daten normalisieren (Claude Code oder Copilot CLI)
	s := normalize(input)

	// Modellspezifische Wochenlimits parallel nachladen (nur Claude Code mit Abo-Session)
	var scoped []ScopedLimit
	usageNote := "skipped"
	scopedDone := make(chan struct{})
	if s.Flavor == FlavorClaude && input.RateLimits != nil {
		go func() {
			scoped, usageNote = fetchScopedLimits()
			close(scopedDone)
		}()
	} else {
		close(scopedDone)
	}

	// Terminalbreite ermitteln
	termWidth := getTerminalWidth()

	// DEBUG: in Datei loggen (stört Claude Code nicht)
	if f, err := os.OpenFile(filepath.Join(os.TempDir(), "statusline-debug.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err == nil {
		stderrW, _, stderrErr := term.GetSize(int(os.Stderr.Fd()))
		tputOut, _ := exec.Command("tput", "cols").Output()
		ttyW, ttyErr := 0, error(nil)
		if tty, err := os.Open("/dev/tty"); err == nil {
			ttyW, _, ttyErr = term.GetSize(int(tty.Fd()))
			tty.Close()
		} else {
			ttyErr = err
		}
		fmt.Fprintf(f, "termWidth=%d COLUMNS=%q stderrW=%d stderrErr=%v ttyW=%d ttyErr=%v tput=%q TERM=%q\n",
			termWidth, os.Getenv("COLUMNS"), stderrW, stderrErr, ttyW, ttyErr, strings.TrimSpace(string(tputOut)), os.Getenv("TERM"))
		f.Close()
	}

	if termWidth < 40 {
		// Minimal-Fallback bei extrem schmalen Terminals
		fmt.Println(BrightMagenta + Bold + s.ModelName + Reset)
		fmt.Println(IconFolder + " " + Cyan + shortenPathTo(s.Cwd, 30) + Reset)
		fmt.Println(IconSession + " " + Dim + "..." + Reset)
		return
	}

	// Externe Daten holen
	gitData := getGitData(resolveWorkDir(s.Cwd))
	cpuPercent, memPercent := getSystemStats()
	cacheNote := "skipped"
	if s.Flavor == FlavorClaude {
		s.Cache.ExpiresAt, cacheNote = readCacheExpiry(input.TranscriptPath)
	}
	sessionDur := s.SessionDur
	if sessionDur == 0 {
		sessionDur = getSessionDuration()
	}

	<-scopedDone
	s.Usage.Scoped = scoped
	if f, err := os.OpenFile(filepath.Join(os.TempDir(), "statusline-debug.log"), os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		fmt.Fprintf(f, "usage=%s scoped=%d cache=%s hit=%d\n", usageNote, len(scoped), cacheNote, s.Cache.HitPct)
		f.Close()
	}

	// 3 Zeilen rendern
	line1 := renderLine1(s, termWidth)
	line2 := renderLine2(s.Cwd, gitData, s.WorktreeName, termWidth)
	line3 := renderLine3(s, cpuPercent, memPercent, sessionDur, termWidth)

	fmt.Println(line1)
	fmt.Println(line2)
	fmt.Println(line3)
}


// formatDuration formatiert eine Dauer als "2h 30m", ab einem Tag als "2d 5h"
func formatDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d >= 24*time.Hour {
		days := d / (24 * time.Hour)
		d -= days * 24 * time.Hour
		return fmt.Sprintf("%dd %dh", days, d/time.Hour)
	}
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute

	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// weekdayShort sind die deutschen Wochentagskürzel, indiziert über time.Weekday
var weekdayShort = [7]string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}

// formatResetClock formatiert einen Reset-Zeitpunkt als Uhrzeit; liegt er mindestens
// einen Tag in der Zukunft, kommt der Wochentag davor ("Do 09:59") — sonst wäre bei
// Wochenlimits nicht erkennbar, welcher Tag gemeint ist.
func formatResetClock(t time.Time) string {
	local := t.Local()
	if time.Until(t) >= 24*time.Hour {
		return weekdayShort[local.Weekday()] + " " + local.Format("15:04")
	}
	return local.Format("15:04")
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

// getColorForProjection gibt die Ampel-Farbe für eine Hochrechnung zurück. Eigene
// Schwellen statt getColorForPercentage: eine Prognose von 60% heißt "hält locker",
// während die normale Ampel dort schon amber wäre.
func getColorForProjection(projected int) string {
	switch {
	case projected > 110:
		return BrightRed
	case projected > 90:
		return Amber
	default:
		return LightGreen
	}
}

// ansiRegex entfernt ANSI Escape-Sequences aus Strings
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// wideEmojis enthält die im Projekt verwendeten Emojis (2 Spalten breit)
var wideEmojis = map[rune]bool{
	'🧠': true, '💻': true, '🎛': true, '💰': true, '⏳': true,
	'🌿': true, '📂': true, '⏱': true, '📅': true, '⚡': true,
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

	// 2. stderr-Fd abfragen — Claude Code pipt nur stdout, stderr bleibt ggf. am TTY
	if w, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && w > 0 {
		return w
	}

	// 3. Controlling Terminal direkt abfragen — greift auch wenn stdout UND stderr
	//    gepipet sind (Copilot CLI pipet beide). Muss vor `tput cols` stehen: tput
	//    liefert ohne TTY still den terminfo-Default (meist 80) statt eines Fehlers.
	if runtime.GOOS != "windows" {
		if f, err := os.Open("/dev/tty"); err == nil {
			defer f.Close()
			if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
				return w
			}
		}
	}

	// 4. tput cols — funktioniert in Mintty/Git Bash wo Windows Console-APIs versagen
	if out, err := exec.Command("tput", "cols").Output(); err == nil {
		if w, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && w > 0 {
			return w
		}
	}

	// 5. Windows Console-Handle als weiterer Fallback
	if runtime.GOOS == "windows" {
		if f, err := os.Open("CONOUT$"); err == nil {
			defer f.Close()
			if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
				return w
			}
		}
	}

	// 6. Fallback
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

// trailingElement rendert ein Element rechts der Context-Anzeige
type trailingElement func(budget int, showBars bool) string

// Fensterlängen der Rate Limits — aus resets_at minus Fensterlänge ergibt sich der Fensterstart
const (
	fiveHourWindow = 5 * time.Hour
	weeklyWindow   = 7 * 24 * time.Hour
)

// minElapsedFraction ist der Anteil des Fensters, der verstrichen sein muss, bevor eine
// Hochrechnung gezeigt wird. Davor wird der Schnitt von einem einzelnen Burst dominiert
// und die Prognose explodiert.
const minElapsedFraction = 0.05

// projectUsage rechnet den bisherigen Durchschnittsverbrauch des Fensters auf das
// Fensterende hoch: verbraucht% geteilt durch den verstrichenen Fensteranteil.
// -1 = keine belastbare Prognose (kein Reset bekannt, Reset vorbei, Fenster zu frisch).
func projectUsage(pct int, resetTs int64, window time.Duration) int {
	if pct < 0 || resetTs <= 0 || window <= 0 {
		return -1
	}
	remaining := time.Until(time.Unix(resetTs, 0))
	if remaining <= 0 || remaining > window {
		return -1
	}
	elapsed := float64(window-remaining) / float64(window)
	if elapsed < minElapsedFraction {
		return -1
	}
	projected := int(float64(pct)/elapsed + 0.5)
	if projected > 999 {
		projected = 999
	}
	return projected
}

// line1Trailing liefert die flavor-spezifischen Elemente rechts der Context-Anzeige, in Anzeigereihenfolge.
// Claude Code: 5h, 7d, dann modellspezifische Wochenlimits (z.B. Fable). Copilot CLI: AIU-Verbrauch + geänderte Zeilen.
func line1Trailing(s Statusline) []trailingElement {
	var elems []trailingElement
	if s.Flavor == FlavorCopilot {
		if s.AIU != "" {
			elems = append(elems, func(budget int, _ bool) string { return renderAIUElement(s.AIU, budget) })
		}
		elems = append(elems, func(_ int, _ bool) string { return renderLinesElement(s.LinesAdded, s.LinesRemoved) })
		return elems
	}

	elems = append(elems, func(budget int, showBars bool) string {
		projected := projectUsage(s.Usage.FiveHourUtil, s.Usage.FiveHourReset, fiveHourWindow)
		return renderRateLimitElement(s.Usage.FiveHourUtil, s.Usage.FiveHourReset, projected, "5h", IconClock, budget, showBars, BrightMagenta)
	})
	if s.Usage.SevenDayUtil >= 0 {
		elems = append(elems, func(budget int, showBars bool) string {
			projected := projectUsage(s.Usage.SevenDayUtil, s.Usage.SevenDayReset, weeklyWindow)
			return renderRateLimitElement(s.Usage.SevenDayUtil, s.Usage.SevenDayReset, projected, "7d", IconCalendar, budget, showBars, BrightYellow)
		})
	}
	for _, sl := range s.Usage.Scoped {
		elems = append(elems, func(budget int, showBars bool) string {
			projected := projectUsage(sl.Util, sl.Reset, weeklyWindow)
			return renderRateLimitElement(sl.Util, sl.Reset, projected, sl.Name, IconCalendar, budget, showBars, Cyan)
		})
	}
	return elems
}

// renderLine1 rendert Zeile 1: Model + Context + Rate Limits bzw. AIU
func renderLine1(s Statusline, termWidth int) string {
	showBars := termWidth >= 100
	sep := makeSep()

	// Breitenregel: <80 nur das erste Element, <120 höchstens zwei, ab 120 drei, ...
	elems := line1Trailing(s)
	maxElems := max(1, termWidth/40)
	if len(elems) > maxElems {
		elems = elems[:maxElems]
	}

	// Model bekommt was es braucht
	modelStr := BrightMagenta + Bold + s.ModelName + Reset
	modelWidth := visibleWidth(modelStr)

	numSeps := 1 + len(elems) // Model•Context + je ein Separator pro Element
	available := termWidth - (numSeps * sepVisibleWidth) - modelWidth
	ctxBudget, budgets := splitLine1Budget(available, len(elems))

	parts := []string{modelStr}
	parts = append(parts, renderContextElement(s.CurrentTokens, s.ContextSize, ctxBudget, showBars))
	for i, el := range elems {
		parts = append(parts, el(budgets[i], showBars))
	}

	line := strings.Join(parts, sep)
	return truncateToWidth(line, termWidth)
}

// splitLine1Budget verteilt die verfügbare Breite auf Context + n Trailing-Elemente.
// 0/1/2 Elemente behalten die bisherigen Anteile (100 | 55/40 | 40/30/20), ab 3 gilt
// Gewichtung Context 4 : erstes Element 3 : weitere je 2. Rundungsrest geht an Context.
func splitLine1Budget(available, n int) (ctx int, elems []int) {
	elems = make([]int, n)
	switch n {
	case 0:
		ctx = available
	case 1:
		ctx, elems[0] = available*55/100, available*40/100
	case 2:
		ctx, elems[0], elems[1] = available*40/100, available*30/100, available*20/100
	default:
		total := 4 + 3 + 2*(n-1)
		ctx = available * 4 / total
		elems[0] = available * 3 / total
		for i := 1; i < n; i++ {
			elems[i] = available * 2 / total
		}
	}
	used := ctx
	for _, b := range elems {
		used += b
	}
	ctx += available - used
	return
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

// renderLine3 rendert Zeile 3: System Stats + Session + Cost bzw. API-Zeit
func renderLine3(s Statusline, cpuPct, memPct float64, sessionDur time.Duration, termWidth int) string {
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

	// Prompt-Cache-Restwaerme (nur Claude Code, Copilot liefert kein Transcript)
	if s.Flavor == FlavorClaude && termWidth >= 70 {
		if el := renderCacheElement(s.Cache, termWidth >= 100); el != "" {
			parts = append(parts, lineElement{el})
		}
	}

	// Cost (Claude Code) bzw. API-Zeit (Copilot CLI kennt keine USD-Kosten)
	if s.Flavor == FlavorCopilot {
		if s.APIDuration > 0 {
			parts = append(parts, lineElement{
				fmt.Sprintf("%s %sAPI %s%s", IconClock, BrightYellow, formatAPIDuration(s.APIDuration), Reset),
			})
		}
	} else if s.CostUSD > 0 {
		parts = append(parts, lineElement{
			fmt.Sprintf("%s %s$%.2f%s", IconCost, BrightYellow, s.CostUSD, Reset),
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
func renderRateLimitElement(pct int, resetTs int64, projected int, label, icon string, budget int, showBars bool, labelColor string) string {
	if pct < 0 {
		return Dim + icon + " " + label + ": N/A" + Reset
	}

	color := getColorForPercentage(pct)

	if showBars && budget >= 20 {
		// Bar + optionaler Zusatz: ⏱(2) + " 5h:"(4) + " "(1) + bar + " "(1) + pct(~3) ≈ 11 + barWidth.
		// Der Platz für die Hochrechnung wird der Bar vorweg abgezogen — sonst frisst sie
		// das ganze Budget und die Prognose fällt gerade bei den schmalen Elementen weg.
		barWidth := budget - 14 - projectionWidth(projected)
		if barWidth < 3 {
			barWidth = 3
		}
		if barWidth > 15 {
			barWidth = 15
		}
		result := renderProgressBar(pct, 100, icon+" "+label, barWidth, labelColor)
		return result + fitSuffix(result, resetTs, projected, budget)
	}

	if budget >= 10 {
		// Kompakt: "⏱ 5h: 45% 12:30" ≈ 10 + optional 6 für Zeit
		result := fmt.Sprintf("%s%s %s:%s %s%d%%%s",
			labelColor, icon, label, Reset,
			color, pct, Reset,
		)
		return result + fitSuffix(result, resetTs, projected, budget)
	}

	// Minimal: "⏱ 5h: 45%"
	return fmt.Sprintf("%s%s %s:%s %s%d%%%s",
		labelColor, icon, label, Reset,
		color, pct, Reset,
	)
}

// renderProjection rendert die Hochrechnung als " →74%"
func renderProjection(projected int) string {
	return fmt.Sprintf(" %s→%d%%%s", getColorForProjection(projected), projected, Reset)
}

// projectionWidth ist die sichtbare Breite der Hochrechnung, 0 wenn es keine gibt
func projectionWidth(projected int) int {
	if projected < 0 {
		return 0
	}
	return visibleWidth(renderProjection(projected))
}

// fitSuffix wählt den ausführlichsten Zusatz (Reset-Zeit + Hochrechnung), der hinter
// result noch ins Budget passt. Fehlende Bausteine sind leere Strings, die Liste
// degradiert dadurch von selbst korrekt.
func fitSuffix(result string, resetTs int64, projected, budget int) string {
	var long, short, proj string
	if resetTs > 0 {
		resetTime := time.Unix(resetTs, 0)
		if timeUntil := time.Until(resetTime); timeUntil > 0 {
			clock := formatResetClock(resetTime)
			long = fmt.Sprintf(" %sin %s (%s)%s", Dim, formatDuration(timeUntil), clock, Reset)
			short = fmt.Sprintf(" %s%s%s", Dim, clock, Reset)
		}
	}
	if projected >= 0 {
		proj = renderProjection(projected)
	}

	// Absteigend nach Ausführlichkeit: die Hochrechnung überlebt länger als die Reset-Zeit
	used := visibleWidth(result)
	for _, candidate := range []string{long + proj, short + proj, proj, long, short} {
		if used+visibleWidth(candidate) <= budget {
			return candidate
		}
	}
	return ""
}

// renderAIUElement rendert den AIU-Verbrauch (Copilot CLI)
func renderAIUElement(formatted string, budget int) string {
	labeled := fmt.Sprintf("%s%s AIU:%s %s", BrightYellow, IconAIU, Reset, formatted)
	if visibleWidth(labeled) <= budget {
		return labeled
	}

	// Minimal: "⚡ 1.2M"
	return fmt.Sprintf("%s%s%s %s", BrightYellow, IconAIU, Reset, formatted)
}

// renderLinesElement rendert die geänderten Zeilen der Session (Copilot CLI)
func renderLinesElement(added, removed int) string {
	if added == 0 && removed == 0 {
		return Dim + "+0/-0" + Reset
	}
	return fmt.Sprintf("%s+%d%s/%s-%d%s", BrightGreen, added, Reset, BrightRed, removed, Reset)
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

// renderCacheElement zeigt, wie lange der Prompt-Cache der Konversation noch warm ist,
// optional mit der Trefferquote des letzten API-Calls. Ohne bekannte TTL faellt es weg.
func renderCacheElement(c CacheInfo, showHit bool) string {
	if c.ExpiresAt.IsZero() {
		return ""
	}
	rest := time.Until(c.ExpiresAt)
	if rest <= 0 {
		return IconCache + " " + Dim + "kalt" + Reset
	}

	label := fmt.Sprintf("%dm", int(rest/time.Minute))
	if rest < time.Minute {
		label = "<1m"
	}
	color := DarkGreen
	switch {
	case rest < 5*time.Minute:
		color = BrightRed
	case rest < 15*time.Minute:
		color = Amber
	}

	out := fmt.Sprintf("%s %s%s%s", IconCache, color, label, Reset)
	if showHit && c.HitPct >= 0 {
		out += fmt.Sprintf(" %s%d%%%s", Dim, c.HitPct, Reset)
	}
	return out
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

// formatAPIDuration formatiert die API-Zeit; unter einer Minute in Sekunden
func formatAPIDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return formatSessionDuration(d)
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

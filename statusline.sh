#!/bin/bash

# ============================================================================
# Claude Code Status Line - Zweizeilige Anzeige
# Zeile 1: Context Size, 5h Limit, Weekly Limit (als Progress Bars)
# Zeile 2: CWD, ausführlicher Git Status
# ============================================================================

# Debug-Modus (setze auf 1 für Debug-Ausgaben in /tmp/statusline_debug.log)
DEBUG=${STATUSLINE_DEBUG:-0}
DEBUG_LOG="/tmp/statusline_debug.log"

# Debug-Funktion
debug() {
    if [ "$DEBUG" -eq 1 ]; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" >> "$DEBUG_LOG"
    fi
}

# Cache-Konfiguration für Debouncing (max 1 Anfrage pro Minute)
CACHE_DIR="$HOME/.claude/cache"
CACHE_FILE="$CACHE_DIR/statusline_cache.json"
USAGE_PERSIST_CACHE="$CACHE_DIR/usage_persist_cache.txt"
CACHE_DURATION=60  # Sekunden

# Farben (für Terminal-Ausgabe)
RESET='\033[0m'
DIM='\033[2m'
CYAN='\033[36m'
GREEN='\033[32m'
YELLOW='\033[33m'
RED='\033[31m'
BLUE='\033[34m'
MAGENTA='\033[35m'
BOLD='\033[1m'

# Intensive/Bright Farben für Labels
BRIGHT_CYAN='\033[96m'
BRIGHT_MAGENTA='\033[95m'
BRIGHT_YELLOW='\033[93m'
BRIGHT_GREEN='\033[92m'

# Ampel-System Farben (mehr Abstufungen)
DARK_GREEN='\033[32m'      # 0-30%: Dunkelgrün
LIGHT_GREEN='\033[92m'     # 31-50%: Hellgrün
AMBER='\033[93m'           # 51-70%: Gelb/Amber
ORANGE='\033[38;5;208m'    # 71-85%: Orange
BRIGHT_RED='\033[91m'      # 86-100%: Hellrot

# Symbole/Icons
ICON_BRAIN="🧠"
ICON_CLOCK="⏱"
ICON_CALENDAR="📅"
ICON_FOLDER="📂"
ICON_GIT="⎇"

# Cache-Verzeichnis erstellen
mkdir -p "$CACHE_DIR"

# JSON-Input von stdin lesen
INPUT=$(cat)
debug "Skript gestartet"

# ============================================================================
# PROGRESS BAR FUNKTION
# ============================================================================
render_progress_bar() {
    local current=$1
    local max=$2
    local label=$3
    local width=${4:-15}
    local label_color=${5:-$BOLD}

    # Prüfe auf N/A oder leere Werte
    if [ "$max" == "N/A" ] || [ "$current" == "N/A" ] || [ -z "$max" ] || [ -z "$current" ]; then
        printf "${DIM}${label}: N/A${RESET}"
        return
    fi

    # Prüfe ob max eine Zahl ist
    if ! [[ "$max" =~ ^[0-9]+$ ]]; then
        printf "${DIM}${label}: N/A${RESET}"
        return
    fi

    # Prüfe ob max 0 ist
    if [ "$max" -eq 0 ]; then
        printf "${DIM}${label}: N/A${RESET}"
        return
    fi

    # Sichere Konvertierung zu Zahlen
    current=${current:-0}
    max=${max:-1}

    local percentage=$((current * 100 / max))
    local filled=$((current * width / max))
    [ $filled -gt $width ] && filled=$width
    local empty=$((width - filled))

    # Farbe basierend auf Auslastung (5 Abstufungen)
    local color=$DARK_GREEN
    if [ $percentage -gt 85 ]; then
        color=$BRIGHT_RED
    elif [ $percentage -gt 70 ]; then
        color=$ORANGE
    elif [ $percentage -gt 50 ]; then
        color=$AMBER
    elif [ $percentage -gt 30 ]; then
        color=$LIGHT_GREEN
    fi

    printf "${label_color}${label}:${RESET} ${color}"
    for ((i=0; i<filled; i++)); do printf "█"; done
    for ((i=0; i<empty; i++)); do printf "░"; done
    printf "${RESET} ${color}${percentage}%%${RESET}"
}

# ============================================================================
# HILFSFUNKTIONEN FÜR FORMATIERUNG
# ============================================================================

# Funktion: Formatiere relative Zeit (Sekunden -> "2h 30m" oder "45m")
format_relative_time() {
    local seconds=$1

    if [ "$seconds" -lt 0 ]; then
        echo "0m"
        return
    fi

    local days=$((seconds / 86400))
    local hours=$(( (seconds % 86400) / 3600 ))
    local minutes=$(( (seconds % 3600) / 60 ))

    if [ $days -gt 0 ]; then
        echo "${days}d ${hours}h"
    elif [ $hours -gt 0 ]; then
        echo "${hours}h ${minutes}m"
    else
        echo "${minutes}m"
    fi
}

# Funktion: Parse ISO 8601 Timestamp zu Unix Epoch (mit UTC-Konvertierung)
parse_iso8601() {
    local iso_time="$1"
    # Parse den vollständigen ISO 8601 String (inkl. Zeitzone)
    # date kann Mikrosekunden (.123) und Zeitzone (+00:00) direkt verarbeiten
    # WICHTIG: Nicht den String kürzen, sonst geht die Zeitzone verloren!
    date -d "$iso_time" +%s 2>/dev/null || echo 0
}

# Funktion: Formatiere ISO 8601 zu "12:59" (nur Zeit, in lokaler Zeitzone)
format_time_only() {
    local iso_time="$1"
    local epoch=$(parse_iso8601 "$iso_time")
    # Konvertiere von UTC zu lokaler Zeit
    date -d "@$epoch" +%H:%M 2>/dev/null || echo "??"
}

# Funktion: Formatiere ISO 8601 zu "19. Jan 07:59" (Datum + Zeit, in lokaler Zeitzone)
format_datetime() {
    local iso_time="$1"
    local epoch=$(parse_iso8601 "$iso_time")
    # Konvertiere von UTC zu lokaler Zeit
    date -d "@$epoch" "+%d. %b %H:%M" 2>/dev/null || echo "??"
}

# Funktion: Formatiere Zahl mit k-Suffix
format_with_k_suffix() {
    local num=$1
    if [ -z "$num" ] || [ "$num" == "N/A" ]; then
        echo "N/A"
        return
    fi

    if [ "$num" -ge 1000 ]; then
        echo "$((num / 1000))k"
    else
        echo "$num"
    fi
}

# Funktion: Rendere Progress Bar mit absoluten Werten
render_progress_bar_with_values() {
    local current=$1
    local max=$2
    local label=$3
    local label_color=$4
    local width=${5:-15}

    # N/A Handling
    if [ "$max" == "N/A" ] || [ "$current" == "N/A" ] || [ -z "$max" ] || [ -z "$current" ]; then
        printf "${DIM}${label}: N/A${RESET}"
        return
    fi

    if ! [[ "$max" =~ ^[0-9]+$ ]] || [ "$max" -eq 0 ]; then
        printf "${DIM}${label}: N/A${RESET}"
        return
    fi

    # Formatiere Werte
    local current_fmt=$(format_with_k_suffix "$current")
    local max_fmt=$(format_with_k_suffix "$max")

    # Berechne Prozentsatz
    local percentage=$((current * 100 / max))
    local filled=$((current * width / max))
    [ $filled -gt $width ] && filled=$width
    local empty=$((width - filled))

    # Wert-Farbe nach Ampelsystem (5 Abstufungen)
    local value_color=$DARK_GREEN
    if [ $percentage -gt 85 ]; then
        value_color=$BRIGHT_RED
    elif [ $percentage -gt 70 ]; then
        value_color=$ORANGE
    elif [ $percentage -gt 50 ]; then
        value_color=$AMBER
    elif [ $percentage -gt 30 ]; then
        value_color=$LIGHT_GREEN
    fi

    # Ausgabe: Label + Werte + Bar + Prozent
    printf "${label_color}${label}:${RESET} ${current_fmt}/${max_fmt} ${value_color}"
    for ((i=0; i<filled; i++)); do printf "█"; done
    for ((i=0; i<empty; i++)); do printf "░"; done
    printf "${RESET} ${value_color}${percentage}%%${RESET}"
}

# ============================================================================
# CACHE-VERWALTUNG
# ============================================================================
should_refresh_cache() {
    if [ ! -f "$CACHE_FILE" ]; then
        return 0  # Cache existiert nicht
    fi

    local cache_time=$(stat -c %Y "$CACHE_FILE" 2>/dev/null || echo 0)
    local current_time=$(date +%s)
    local cache_age=$((current_time - cache_time))

    if [ $cache_age -gt $CACHE_DURATION ]; then
        return 0  # Cache ist zu alt (älter als 60 Sekunden)
    fi

    return 1  # Cache ist noch gültig
}

# ============================================================================
# USAGE LIMITS ERMITTELN - Via Anthropic OAuth API
# ============================================================================

# API Konfiguration
API_ENDPOINT="https://api.anthropic.com/api/oauth/usage"
ANTHROPIC_BETA="oauth-2025-04-20"

# Funktion: Token aus Keychain oder Datei extrahieren
get_access_token() {
    local creds_file="$HOME/.claude/.credentials.json"
    local token

    debug "get_access_token: Starte Token-Extraktion"

    # Methode 1: Aus .credentials.json Datei lesen (Linux default)
    # WICHTIG: Kein cat verwenden, da stdin bereits vom INPUT=$(cat) verbraucht wurde!
    if [[ -f "$creds_file" ]]; then
        debug "get_access_token: .credentials.json gefunden"
        token=$(jq -r '.claudeAiOauth.accessToken' "$creds_file" 2>/dev/null)
        if [[ -n "$token" ]] && [[ "$token" != "null" ]]; then
            debug "get_access_token: Token erfolgreich extrahiert (Länge: ${#token})"
            echo "$token"
            return 0
        else
            debug "get_access_token: Token ist leer oder null"
        fi
    else
        debug "get_access_token: .credentials.json nicht gefunden"
    fi

    # Methode 2: Aus System Keychain (Linux mit libsecret)
    if [[ "$OSTYPE" == "linux-gnu"* ]] && command -v secret-tool &> /dev/null; then
        token=$(secret-tool lookup service "Claude Code-credentials" 2>/dev/null | jq -r '.claudeAiOauth.accessToken' 2>/dev/null)
        if [[ -n "$token" ]] && [[ "$token" != "null" ]]; then
            echo "$token"
            return 0
        fi
    fi

    # Methode 3: macOS Keychain
    if [[ "$OSTYPE" == "darwin"* ]]; then
        token=$(security find-generic-password -s "Claude Code-credentials" -w 2>/dev/null | jq -r '.claudeAiOauth.accessToken' 2>/dev/null)
        if [[ -n "$token" ]] && [[ "$token" != "null" ]]; then
            echo "$token"
            return 0
        fi
    fi

    return 1
}

# Funktion: Usage-Daten von API abrufen
fetch_usage_from_api() {
    local access_token
    access_token=$(get_access_token)

    debug "fetch_usage_from_api: Starte API-Call"

    # Token-Validierung
    if [[ -z "$access_token" ]] || [[ "$access_token" == "null" ]]; then
        debug "fetch_usage_from_api: Kein gültiger Token verfügbar"
        return 1
    fi

    debug "fetch_usage_from_api: Token validiert, rufe API auf..."

    # API-Call mit timeout von 2 Sekunden
    local response
    local start_time=$(date +%s%3N)
    response=$(curl -s -f --max-time 2 -X GET "$API_ENDPOINT" \
        -H "Accept: application/json" \
        -H "Authorization: Bearer $access_token" \
        -H "anthropic-beta: $ANTHROPIC_BETA" 2>/dev/null)

    local curl_exit_code=$?
    local end_time=$(date +%s%3N)
    local duration=$((end_time - start_time))

    debug "fetch_usage_from_api: curl beendet mit exit code $curl_exit_code (Dauer: ${duration}ms)"

    # Prüfe ob curl erfolgreich war
    if [[ $curl_exit_code -ne 0 ]]; then
        debug "fetch_usage_from_api: curl fehlgeschlagen"
        return 1
    fi

    # Validiere Response (muss five_hour und seven_day enthalten)
    if ! echo "$response" | jq -e '.five_hour, .seven_day' &>/dev/null; then
        debug "fetch_usage_from_api: Ungültige API-Response: $response"
        return 1
    fi

    debug "fetch_usage_from_api: API-Call erfolgreich"

    echo "$response"
    return 0
}

get_usage_limits() {
    debug "get_usage_limits: Starte Abruf"

    local current_time=$(date +%s)
    local cache_timestamp=0
    local cached_data=""

    # SCHRITT 1: Lies Cache und parse Timestamp
    if [[ -f "$USAGE_PERSIST_CACHE" ]]; then
        cache_timestamp=$(head -n 1 "$USAGE_PERSIST_CACHE" 2>/dev/null || echo 0)
        cached_data=$(tail -n 1 "$USAGE_PERSIST_CACHE" 2>/dev/null)
    fi

    local cache_age=$((current_time - cache_timestamp))

    # SCHRITT 2: Wenn Cache frisch (< 60s), return gecachte Daten
    if [ $cache_age -lt 60 ] && [[ -n "$cached_data" ]]; then
        debug "get_usage_limits: Cache gültig (${cache_age}s alt): $cached_data"
        echo "$cached_data"
        return 0
    fi

    # SCHRITT 3: Cache abgelaufen - API-Call
    debug "get_usage_limits: Cache abgelaufen (${cache_age}s), rufe API auf"
    local api_data
    api_data=$(fetch_usage_from_api 2>/dev/null)

    # Wenn API-Abruf erfolgreich, parse und speichere mit Timestamp
    if [[ -n "$api_data" ]] && echo "$api_data" | jq -e '.five_hour, .seven_day' &>/dev/null; then
        debug "get_usage_limits: API-Call erfolgreich"

        # Parse API Response (inkl. Reset-Zeiten)
        local five_hour_util=$(echo "$api_data" | jq -r '.five_hour.utilization // "N/A"')
        local seven_day_util=$(echo "$api_data" | jq -r '.seven_day.utilization // "N/A"')
        local five_hour_reset=$(echo "$api_data" | jq -r '.five_hour.resets_at // "N/A"')
        local seven_day_reset=$(echo "$api_data" | jq -r '.seven_day.resets_at // "N/A"')

        # Runde auf Ganzzahlen
        if [[ "$five_hour_util" =~ ^[0-9]+\.[0-9]+$ ]]; then
            five_hour_util=$(printf "%.0f" "$five_hour_util")
        fi
        if [[ "$seven_day_util" =~ ^[0-9]+\.[0-9]+$ ]]; then
            seven_day_util=$(printf "%.0f" "$seven_day_util")
        fi

        # Format: util:max:util:max|reset_5h|reset_weekly (| als Trenner für Timestamps)
        local result="$five_hour_util:100:$seven_day_util:100|$five_hour_reset|$seven_day_reset"

        # Speichere mit Timestamp (Zeile 1: Timestamp, Zeile 2: Daten)
        cat > "$USAGE_PERSIST_CACHE" <<EOF
$current_time
$result
EOF

        debug "get_usage_limits: Gespeichert mit Timestamp $current_time: $result"
        echo "$result"
    else
        debug "get_usage_limits: API fehlgeschlagen, verwende alten Cache falls vorhanden"
        # API fehlgeschlagen - verwende alten Cache (egal wie alt)
        if [[ -n "$cached_data" ]]; then
            debug "get_usage_limits: Verwende alten Cache (${cache_age}s alt): $cached_data"
            echo "$cached_data"
            return 0
        fi

        debug "get_usage_limits: Kein Cache verfügbar, gebe N/A zurück"
        echo "N/A:N/A:N/A:N/A"
    fi
}

# ============================================================================
# GIT STATUS ERMITTELN (mit --no-optional-locks für Performance)
# ============================================================================
get_git_status() {
    # Prüfe ob wir in einem Git-Repository sind
    if ! git -c core.useBuiltinFSMonitor=false rev-parse --git-dir >/dev/null 2>&1; then
        # Außerhalb von Git: Zeige alle Kategorien mit N/A
        echo "${DIM}Änderungen: N/A | Staged: N/A | Stash: N/A | Unpushed: N/A | Unpulled: N/A${RESET}"
        return
    fi

    local status_parts=()

    # 1. Uncommitted changes (staged + unstaged) - IMMER anzeigen
    local changes=$(git -c core.useBuiltinFSMonitor=false --no-optional-locks status --porcelain 2>/dev/null | wc -l)
    if [ "$changes" -gt 0 ]; then
        status_parts+=("${YELLOW}Änderungen: ${changes}${RESET}")
    else
        status_parts+=("${DIM}Änderungen: 0${RESET}")
    fi

    # 2. Staged changes - IMMER anzeigen
    local staged=$(git -c core.useBuiltinFSMonitor=false --no-optional-locks diff --cached --numstat 2>/dev/null | wc -l)
    if [ "$staged" -gt 0 ]; then
        status_parts+=("${BRIGHT_GREEN}Staged: ${staged}${RESET}")
    else
        status_parts+=("${DIM}Staged: 0${RESET}")
    fi

    # 3. Stash count - IMMER anzeigen
    local stash_count=$(git -c core.useBuiltinFSMonitor=false stash list 2>/dev/null | wc -l)
    if [ "$stash_count" -gt 0 ]; then
        status_parts+=("${CYAN}Stash: ${stash_count}${RESET}")
    else
        status_parts+=("${DIM}Stash: 0${RESET}")
    fi

    # 4. Unpushed/Unpulled commits - IMMER anzeigen
    local branch=$(git -c core.useBuiltinFSMonitor=false symbolic-ref --short HEAD 2>/dev/null)
    local upstream=$(git -c core.useBuiltinFSMonitor=false rev-parse --abbrev-ref @{u} 2>/dev/null)

    if [ -n "$upstream" ]; then
        # Unpushed commits
        local unpushed=$(git -c core.useBuiltinFSMonitor=false --no-optional-locks rev-list --count @{u}..HEAD 2>/dev/null)
        if [ "$unpushed" -gt 0 ]; then
            status_parts+=("${BRIGHT_GREEN}Unpushed: ${unpushed}${RESET}")
        else
            status_parts+=("${DIM}Unpushed: 0${RESET}")
        fi

        # Unpulled commits
        local unpulled=$(git -c core.useBuiltinFSMonitor=false --no-optional-locks rev-list --count HEAD..@{u} 2>/dev/null)
        if [ "$unpulled" -gt 0 ]; then
            status_parts+=("${BLUE}Unpulled: ${unpulled}${RESET}")
        else
            status_parts+=("${DIM}Unpulled: 0${RESET}")
        fi
    else
        # Kein upstream
        status_parts+=("${DIM}Unpushed: N/A${RESET}")
        status_parts+=("${DIM}Unpulled: N/A${RESET}")
    fi

    # 5. Merge/Rebase in Progress? (nur wenn aktiv)
    if [ -f "$(git rev-parse --git-dir)/MERGE_HEAD" ]; then
        status_parts+=("${BRIGHT_RED}MERGE!${RESET}")
    fi
    if [ -d "$(git rev-parse --git-dir)/rebase-merge" ] || [ -d "$(git rev-parse --git-dir)/rebase-apply" ]; then
        status_parts+=("${BRIGHT_RED}REBASE!${RESET}")
    fi

    # Alle Teile mit " | " verbinden
    local IFS=" ${DIM}|${RESET} "
    echo "${status_parts[*]}"
}

# ============================================================================
# MAIN LOGIC - DATEN SAMMELN
# ============================================================================

# Hole Usage-Daten (mit eigenem internen Caching)
usage_data=$(get_usage_limits)
# Parse Format: 64:100:8:100|2026-01-12T13:00:00+00:00|2026-01-19T08:00:00+00:00
usage_numbers="${usage_data%%|*}"  # Alles vor dem ersten |
usage_timestamps="${usage_data#*|}" # Alles nach dem ersten |
IFS=':' read -r usage_5h usage_5h_max usage_weekly usage_weekly_max <<< "$usage_numbers"
reset_5h=$(echo "$usage_timestamps" | cut -d'|' -f1)
reset_weekly=$(echo "$usage_timestamps" | cut -d'|' -f2)

# Hole Git-Status (mit Caching für Performance)
if should_refresh_cache; then
    git_status=$(get_git_status)
    # Speichere Git-Status im Cache
    cat > "$CACHE_FILE" <<EOF
{
  "git": "$git_status",
  "timestamp": $(date +%s)
}
EOF
else
    # Lade Git-Status aus Cache
    if [ -f "$CACHE_FILE" ]; then
        git_status=$(jq -r '.git // ""' "$CACHE_FILE" 2>/dev/null)
        if [ -z "$git_status" ]; then
            git_status=$(get_git_status)
        fi
    else
        git_status=$(get_git_status)
    fi
fi

# ============================================================================
# CONTEXT WINDOW AUS JSON-INPUT (ohne Caching)
# ============================================================================
context_usage=$(echo "$INPUT" | jq '.context_window.current_usage')

if [ "$context_usage" != "null" ] && [ "$context_usage" != "" ]; then
    # Echte Daten vorhanden - berechne total tokens
    current_tokens=$(echo "$context_usage" | jq '.input_tokens + .cache_creation_input_tokens + .cache_read_input_tokens')
    context_size=$(echo "$INPUT" | jq '.context_window.context_window_size')
else
    # Keine Daten verfügbar - zeige 0
    current_tokens=0
    context_size=$(echo "$INPUT" | jq '.context_window.context_window_size // 200000')
fi

# ============================================================================
# CWD AUS JSON-INPUT
# ============================================================================
cwd=$(echo "$INPUT" | jq -r '.workspace.current_dir // .cwd // "~"')

# Kürze CWD wenn zu lang (ersetze Home mit ~)
cwd="${cwd/#$HOME/~}"

# ============================================================================
# MODELL-NAME AUS JSON-INPUT
# ============================================================================
model_name=$(echo "$INPUT" | jq -r '.model.display_name // "Unknown"')

# ============================================================================
# ZEILE 1: CONTEXT UND USAGE LIMITS
# ============================================================================
line1=""

# Modell-Name am Anfang mit Icon
line1+="${BRIGHT_MAGENTA}${BOLD}${model_name}${RESET}  ${DIM}•${RESET}  "

# Context Window mit Token-Zahlen und Brain-Icon
line1+=$(render_progress_bar_with_values "$current_tokens" "$context_size" "${ICON_BRAIN} Context" "$BRIGHT_CYAN" 15)
line1+="  ${DIM}•${RESET}  "

# 5h Usage Limit mit Clock-Icon und Reset-Zeit
if [ "$usage_5h" != "N/A" ] && [ "$usage_5h_max" != "N/A" ]; then
    line1+=$(render_progress_bar "$usage_5h" "$usage_5h_max" "${ICON_CLOCK} 5h" 12 "$BRIGHT_MAGENTA")

    # Füge Reset-Zeit hinzu: "in 2h 30m (12:59)"
    if [ "$reset_5h" != "N/A" ] && [ -n "$reset_5h" ]; then
        current_epoch=$(date +%s)
        reset_epoch=$(parse_iso8601 "$reset_5h")
        time_until=$((reset_epoch - current_epoch))
        relative=$(format_relative_time "$time_until")
        absolute=$(format_time_only "$reset_5h")
        line1+=" ${DIM}in ${relative} (${absolute})${RESET}"
    fi
else
    line1+="${DIM}${ICON_CLOCK} 5h: N/A${RESET}"
fi
line1+="  ${DIM}•${RESET}  "

# Weekly Usage Limit mit Calendar-Icon und Reset-Zeit
if [ "$usage_weekly" != "N/A" ] && [ "$usage_weekly_max" != "N/A" ]; then
    line1+=$(render_progress_bar "$usage_weekly" "$usage_weekly_max" "${ICON_CALENDAR} 7d" 12 "$BRIGHT_YELLOW")

    # Füge Reset-Zeit hinzu: "19. Jan 07:59"
    if [ "$reset_weekly" != "N/A" ] && [ -n "$reset_weekly" ]; then
        reset_formatted=$(format_datetime "$reset_weekly")
        line1+=" ${DIM}${reset_formatted}${RESET}"
    fi
else
    line1+="${DIM}${ICON_CALENDAR} 7d: N/A${RESET}"
fi

# ============================================================================
# ZEILE 2: CWD UND GIT STATUS
# ============================================================================
line2="${ICON_FOLDER} ${CYAN}${cwd}${RESET}  ${DIM}•${RESET}  ${ICON_GIT} ${git_status}"

# ============================================================================
# AUSGABE MIT ECHO FÜR ANSI-CODES
# ============================================================================
echo -e "${line1}"
echo -e "${line2}"

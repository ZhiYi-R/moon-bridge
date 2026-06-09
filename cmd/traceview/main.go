package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

//go:embed index.html
var static embed.FS

func main() {
	port := flag.Int("port", 19999, "HTTP listen port")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: traceview [-port PORT] <sessions-root-directory>")
		os.Exit(1)
	}
	sessionsRoot := flag.Arg(0)

	http.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		listSessions(sessionsRoot, w)
	})
	http.HandleFunc("/api/traces", func(w http.ResponseWriter, r *http.Request) {
		dir := resolveSession(sessionsRoot, r.URL.Query().Get("session"))
		if dir == "" {
			http.Error(w, "session required", http.StatusBadRequest)
			return
		}
		listTraces(dir, w, r)
	})
	http.HandleFunc("/api/trace/", func(w http.ResponseWriter, r *http.Request) {
		dir := resolveSession(sessionsRoot, r.URL.Query().Get("session"))
		if dir == "" {
			http.Error(w, "session required", http.StatusBadRequest)
			return
		}
		numStr := strings.TrimPrefix(r.URL.Path, "/api/trace/")
		num, err := strconv.Atoi(numStr)
		if err != nil {
			http.Error(w, "invalid trace number", http.StatusBadRequest)
			return
		}
		getTrace(dir, num, w)
	})
	http.Handle("/", http.FileServer(http.FS(static)))

	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	fmt.Printf("Trace viewer: http://0.0.0.0:%d\n", *port)
	openBrowser(fmt.Sprintf("http://localhost:%d", *port))
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func resolveSession(root, name string) string {
	if name == "" {
		return ""
	}
	candidate := filepath.Join(root, filepath.Clean(name))
	if !strings.HasPrefix(filepath.Clean(candidate), filepath.Clean(root)) {
		return ""
	}
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return ""
}

type SessionInfo struct {
	Name       string `json:"name"`
	TraceCount int    `json:"trace_count"`
	LastMod    string `json:"last_modified"`
}

func listSessions(root string, w http.ResponseWriter) {
	entries, err := os.ReadDir(root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var sessions []SessionInfo

	// tryAddSession checks a directory for a Response/ subdirectory and appends it.
	tryAddSession := func(sessPath, name string) {
		respDir := filepath.Join(sessPath, "Response")
		info, err := os.Stat(respDir)
		if err != nil || !info.IsDir() {
			return
		}
		respEntries, err := os.ReadDir(respDir)
		if err != nil {
			return
		}
		count := 0
		for _, re := range respEntries {
			if !re.IsDir() && strings.HasSuffix(re.Name(), ".json") {
				count++
			}
		}
		if count == 0 {
			return
		}
		sessions = append(sessions, SessionInfo{
			Name:       name,
			TraceCount: count,
			LastMod:    info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Level 1: check if this directory itself is a session (has Response/).
		l1Path := filepath.Join(root, e.Name())
		tryAddSession(l1Path, e.Name())
		// Level 2: scan for model subdirectories (e.g. gpt-5.4/).
		l1Entries, err := os.ReadDir(l1Path)
		if err != nil {
			continue
		}
		for _, l2 := range l1Entries {
			if !l2.IsDir() {
				continue
			}
			l2Path := filepath.Join(l1Path, l2.Name())
			tryAddSession(l2Path, e.Name()+"/"+l2.Name())
		}
	}

	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

type TraceInfo struct {
	Number     int    `json:"number"`
	Model      string `json:"model"`
	SessionID  string `json:"session_id"`
	Error      string `json:"error"`
	CapturedAt string `json:"captured_at"`
}

func listTraces(dir string, w http.ResponseWriter, r *http.Request) {
	respDir := filepath.Join(dir, "Response")
	entries, err := os.ReadDir(respDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Collect numbers first (cheap, no file reads).
	var nums []int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		num, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		nums = append(nums, num)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(nums)))

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}

	var traces []TraceInfo
	var page []int
	if offset < len(nums) {
		end := offset + limit
		if end > len(nums) {
			end = len(nums)
		}
		page = nums[offset:end]
	}

	for _, num := range page {
		info := TraceInfo{Number: num}
		data, err := os.ReadFile(filepath.Join(respDir, fmt.Sprintf("%d.json", num)))
		if err != nil {
			traces = append(traces, info)
			continue
		}
		var raw map[string]any
		if json.Unmarshal(data, &raw) == nil {
			if m, ok := raw["model"].(string); ok {
				info.Model = m
			}
			if s, ok := raw["session_id"].(string); ok {
				info.SessionID = s
			}
			if cap, ok := raw["captured_at"].(string); ok {
				info.CapturedAt = cap
			}
			if errObj, ok := raw["error"].(map[string]any); ok {
				if msg, ok := errObj["message"].(string); ok {
					info.Error = msg
				}
			}
		}
		traces = append(traces, info)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"traces":   traces,
		"total":    len(nums),
		"has_more": offset+limit < len(nums),
	})
}

func getTrace(dir string, num int, w http.ResponseWriter) {
	respPath := filepath.Join(dir, "Response", fmt.Sprintf("%d.json", num))
	data, err := os.ReadFile(respPath)
	if err != nil {
		http.Error(w, "trace not found", http.StatusNotFound)
		return
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := map[string]any{
		"number": num,
	}
	for _, k := range []string{"openai_request", "upstream_request", "openai_response", "error", "captured_at", "model"} {
		if v, ok := raw[k]; ok {
			result[k] = v
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	exec.Command(cmd, args...).Start()
}

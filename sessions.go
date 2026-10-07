package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Live Claude Code sessions, read from ~/.claude/sessions/<pid>.json.
// Files of dead sessions linger there, so each one is checked against /proc:
// the pid must exist and its start time must match procStart (pid reuse).

type agentCounts struct {
	busy, waiting, idle int
}

func claudeDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// procStartTime returns field 22 (starttime) of /proc/<pid>/stat.
func procStartTime(pid int) (string, bool) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", false
	}
	// comm (field 2) may contain spaces; fields after it start past the last ')'.
	s := string(raw)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", false
	}
	f := strings.Fields(s[i+1:]) // f[0] is field 3
	if len(f) < 20 {
		return "", false
	}
	return f[19], true
}

func countAgents() agentCounts {
	var c agentCounts
	files, _ := filepath.Glob(filepath.Join(claudeDir(), "sessions", "*.json"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s struct {
			Pid       int    `json:"pid"`
			ProcStart string `json:"procStart"`
			Status    string `json:"status"`
		}
		if json.Unmarshal(raw, &s) != nil || s.Pid == 0 {
			continue
		}
		start, ok := procStartTime(s.Pid)
		if !ok || (s.ProcStart != "" && start != s.ProcStart) {
			continue
		}
		switch s.Status {
		case "busy":
			c.busy++
		case "waiting":
			c.waiting++
		default:
			c.idle++
		}
	}
	return c
}

package audit

import (
	"regexp"
	"strings"
)

// Level is the deterministic risk grade assigned to one tool call.
type Level string

const (
	LevelLow      Level = "low"
	LevelMedium   Level = "medium"
	LevelHigh     Level = "high"
	LevelCritical Level = "critical"
)

// Reason identifies the rule family that raised a tool call's risk level.
type Reason string

const (
	ReasonDestructiveCommand Reason = "destructive_command"
	ReasonPipeToShell        Reason = "pipe_to_shell"
	ReasonSensitivePath      Reason = "sensitive_path"
	ReasonNetworkEgress      Reason = "network_egress"
	ReasonWorkspaceEscape    Reason = "workspace_escape"
)

var levelRank = map[Level]int{
	LevelLow:      0,
	LevelMedium:   1,
	LevelHigh:     2,
	LevelCritical: 3,
}

type riskRule struct {
	reason   Reason
	level    Level
	patterns []*regexp.Regexp
}

// riskRules run in declaration order over the combined tool name and input
// summary. Every matching rule contributes its reason; the highest matching
// level wins. The patterns are deliberately simple substring-level heuristics
// for the first iteration, not a shell parser.
var riskRules = []riskRule{
	{
		reason: ReasonPipeToShell,
		level:  LevelCritical,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`\|\s*(sudo\s+)?(bash|sh|zsh|ksh)\b`),
		},
	},
	{
		reason: ReasonDestructiveCommand,
		level:  LevelCritical,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`\brm\s+(-[a-zA-Z]+\s+)*-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*\b`),
			regexp.MustCompile(`\brm\s+(-[a-zA-Z]+\s+)*-[a-zA-Z]*f[a-zA-Z]*r[a-zA-Z]*\b`),
			regexp.MustCompile(`\brm\s+-[a-zA-Z]*r[a-zA-Z]*\s+-[a-zA-Z]*f`),
			regexp.MustCompile(`\brm\s+-[a-zA-Z]*f[a-zA-Z]*\s+-[a-zA-Z]*r`),
			regexp.MustCompile(`\bgit\s+push\b[^|\n]*(--force\b|\s-f\b)`),
			regexp.MustCompile(`(?i)\b(drop|truncate)\s+(table|database)\b`),
			regexp.MustCompile(`\bmkfs\b`),
			regexp.MustCompile(`\bdd\s+if=`),
		},
	},
	{
		reason: ReasonSensitivePath,
		level:  LevelHigh,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`/\.ssh/`),
			regexp.MustCompile(`\bid_(rsa|ed25519|ecdsa|dsa)\b`),
			regexp.MustCompile(`\.env\b`),
			regexp.MustCompile(`\.aws/credentials`),
			regexp.MustCompile(`\.kube/config`),
			regexp.MustCompile(`\.netrc\b`),
			regexp.MustCompile(`/etc/(shadow|passwd|sudoers)\b`),
			regexp.MustCompile(`\bcredentials\.json\b`),
			regexp.MustCompile(`\.pem\b`),
		},
	},
	{
		reason: ReasonNetworkEgress,
		level:  LevelMedium,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`\b(curl|wget|nc|ncat|netcat|scp|sftp|ssh|ftp|telnet)\s`),
		},
	},
	{
		reason: ReasonWorkspaceEscape,
		level:  LevelHigh,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`>\s*/(etc|usr|var|bin|sbin|boot)/`),
			regexp.MustCompile(`>\s*~/`),
			regexp.MustCompile(`>\s*\.\./`),
			regexp.MustCompile(`(^|[\s>"'])` + `\.\./`),
		},
	},
}

// Classify grades one tool call from its name and input summary. It is a
// pure function: same input, same verdict, no I/O.
func Classify(toolName, inputSummary string) (Level, []Reason) {
	text := strings.TrimSpace(toolName + " " + inputSummary)
	level := LevelLow
	reasons := make([]Reason, 0, len(riskRules))
	for _, rule := range riskRules {
		if !ruleMatches(rule, text) {
			continue
		}
		reasons = append(reasons, rule.reason)
		if levelRank[rule.level] > levelRank[level] {
			level = rule.level
		}
	}
	return level, reasons
}

func ruleMatches(rule riskRule, text string) bool {
	for _, pattern := range rule.patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func isHighRisk(level Level) bool {
	return levelRank[level] >= levelRank[LevelHigh]
}

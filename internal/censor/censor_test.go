package censor

import (
	"strings"
	"testing"

	"github.com/ntalmon/aka/internal/history"
)

// ---- CensorAll ----

func TestCensorAllPreservesTimestamps(t *testing.T) {
	entries := []history.Entry{
		{Timestamp: 100, Command: "git checkout main"},
		{Timestamp: 200, Command: "git checkout feature"},
		{Timestamp: 300, Command: "git checkout bugfix"},
	}
	censored, _ := CensorAll(entries)
	if len(censored) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(censored))
	}
	for i, e := range censored {
		if e.Timestamp != entries[i].Timestamp {
			t.Errorf("entry %d: timestamp lost: want %d got %d", i, entries[i].Timestamp, e.Timestamp)
		}
	}
}

func TestCensorAllPass1RedactsSecrets(t *testing.T) {
	awsKey := "AKIAIOSFODNN7EXAMPLE"
	entries := []history.Entry{
		{Command: "aws s3 ls --key " + awsKey},
	}
	censored, rm := CensorAll(entries)
	if strings.Contains(censored[0].Command, awsKey) {
		t.Errorf("AWS key not censored: %q", censored[0].Command)
	}
	found := false
	for _, v := range rm {
		if v == awsKey {
			found = true
		}
	}
	if !found {
		t.Errorf("AWS key missing from redaction map: %v", rm)
	}
}

func TestCensorAllPass2ParameterizesVariableSlots(t *testing.T) {
	// Varying positional args that contain digits are treated as data and parameterized.
	entries := []history.Entry{
		{Command: "ssh host1"},
		{Command: "ssh host2"},
		{Command: "ssh host3"},
	}
	censored, _ := CensorAll(entries)
	for _, e := range censored {
		if strings.Contains(e.Command, "host1") || strings.Contains(e.Command, "host2") {
			t.Errorf("varying SSH target not parameterized: %q", e.Command)
		}
	}
}

func TestCensorAllGitBranchesNotParameterized(t *testing.T) {
	entries := []history.Entry{
		{Command: "git checkout feature-a"},
		{Command: "git checkout feature-b"},
		{Command: "git checkout feature-c"},
	}
	censored, _ := CensorAll(entries)
	for _, e := range censored {
		if strings.Contains(e.Command, "<") {
			t.Errorf("git branch was incorrectly parameterized: %q", e.Command)
		}
	}
}

func TestCensorAllMergesRedactionMaps(t *testing.T) {
	awsKey := "AKIAIOSFODNN7EXAMPLE"
	entries := []history.Entry{
		{Command: "aws s3 ls --key " + awsKey},
		{Command: "ssh host1"},
		{Command: "ssh host2"},
	}
	_, rm := CensorAll(entries)
	if len(rm) == 0 {
		t.Error("expected non-empty redaction map")
	}
	foundSecret := false
	for _, v := range rm {
		if v == awsKey {
			foundSecret = true
		}
	}
	if !foundSecret {
		t.Errorf("AWS key not in merged redaction map: %v", rm)
	}
}

func TestCensorAllEmptyInput(t *testing.T) {
	censored, rm := CensorAll(nil)
	if len(censored) != 0 {
		t.Errorf("expected empty result for nil input, got %v", censored)
	}
	if len(rm) != 0 {
		t.Errorf("expected empty redaction map for nil input, got %v", rm)
	}
}

func TestCensorAllPreservesCommandCount(t *testing.T) {
	entries := []history.Entry{
		{Command: "git status"},
		{Command: "go build ./..."},
		{Command: "docker ps -a"},
	}
	censored, _ := CensorAll(entries)
	if len(censored) != len(entries) {
		t.Errorf("entry count changed: want %d got %d", len(entries), len(censored))
	}
}

// ---- inferVarType ----

func TestInferVarTypePath(t *testing.T) {
	if got := inferVarType("/home/user/file.go", 0); got != "PATH" {
		t.Errorf("expected PATH for slash-containing value, got %q", got)
	}
	if got := inferVarType("~/projects/foo", 0); got != "PATH" {
		t.Errorf("expected PATH for tilde-prefixed value, got %q", got)
	}
	if got := inferVarType("./relative/path", 0); got != "PATH" {
		t.Errorf("expected PATH for dot-prefixed value, got %q", got)
	}
}

func TestInferVarTypeHost(t *testing.T) {
	if got := inferVarType("db.example.com", 0); got != "HOST" {
		t.Errorf("expected HOST for domain-like value, got %q", got)
	}
}

func TestInferVarTypeBranch(t *testing.T) {
	// Git binary no longer produces a special BRANCH type; falls through to VAR.
	if got := inferVarType("feature-branch", 0); got != "VAR" {
		t.Errorf("expected VAR for git branch-like value, got %q", got)
	}
}

func TestInferVarTypeVarFallback(t *testing.T) {
	if got := inferVarType("somevalue", 0); got != "VAR" {
		t.Errorf("expected VAR fallback, got %q", got)
	}
}

// ---- shannonEntropy ----

func TestShannonEntropyEmptyString(t *testing.T) {
	if got := shannonEntropy(""); got != 0 {
		t.Errorf("expected 0 entropy for empty string, got %f", got)
	}
}

func TestShannonEntropyUniform(t *testing.T) {
	// "aaaa" has 0 entropy.
	if got := shannonEntropy("aaaa"); got != 0 {
		t.Errorf("expected 0 entropy for uniform string, got %f", got)
	}
}

func TestShannonEntropyHighForRandom(t *testing.T) {
	// A high-entropy token should score above the 4.5 threshold.
	token := "aB3xY7qZ2mNpRsLvKwTe" // mixed case + digits, 20 chars
	if got := shannonEntropy(token); got <= 4.0 {
		t.Errorf("expected high entropy (>4.0) for mixed token, got %f", got)
	}
}

// ---- isLikelySecret ----

func TestIsLikelySecretShortStringFalse(t *testing.T) {
	if isLikelySecret("abc123XY") {
		t.Error("short string should not be flagged as secret")
	}
}

func TestIsLikelySecretNoMixedClassFalse(t *testing.T) {
	// Only one char class (lowercase only) — must not be flagged even at high entropy.
	if isLikelySecret("abcdefghijklmnopqrstuvwxyzabcde") {
		t.Error("single-char-class (lowercase only) string should not be flagged as secret")
	}
}

func TestIsLikelySecretHighEntropyMixedTrue(t *testing.T) {
	// Construct a 24-char mixed-case+digit high-entropy string.
	secret := "aB3xY7qZ2mNpRsLvKwTeXj9P"
	if !isLikelySecret(secret) {
		t.Errorf("high-entropy mixed string should be flagged as secret: %q", secret)
	}
}

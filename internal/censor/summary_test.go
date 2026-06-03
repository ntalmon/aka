package censor

import (
	"strings"
	"testing"
)

func TestSummarizeEmpty(t *testing.T) {
	got := Summarize(nil)
	if got != "No sensitive data detected." {
		t.Errorf("expected no-data message, got %q", got)
	}
}

func TestSummarizeEmptyMap(t *testing.T) {
	got := Summarize(map[string]string{})
	if got != "No sensitive data detected." {
		t.Errorf("expected no-data message, got %q", got)
	}
}

func TestSummarizeSingleToken(t *testing.T) {
	rm := map[string]string{"<TOKEN_1>": "sk-ant-abc123"}
	got := Summarize(rm)
	if got != "Masked 1 token." {
		t.Errorf("unexpected summary: %q", got)
	}
}

func TestSummarizeMultipleTokens(t *testing.T) {
	rm := map[string]string{
		"<TOKEN_1>": "sk-ant-abc123",
		"<TOKEN_2>": "ghp_xyz",
	}
	got := Summarize(rm)
	if got != "Masked 2 tokens." {
		t.Errorf("unexpected summary: %q", got)
	}
}

func TestSummarizeMixedCategories(t *testing.T) {
	rm := map[string]string{
		"<TOKEN_1>":    "sk-ant-abc",
		"<PASSWORD_1>": "hunter2",
	}
	got := Summarize(rm)
	if !strings.Contains(got, "1 token") || !strings.Contains(got, "1 password") {
		t.Errorf("unexpected summary: %q", got)
	}
	if !strings.HasPrefix(got, "Masked ") || !strings.HasSuffix(got, ".") {
		t.Errorf("summary format wrong: %q", got)
	}
}

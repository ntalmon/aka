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
		"<TOKEN_1>": "sk-ant-abc",
		"<IP_1>":    "192.168.1.1",
	}
	got := Summarize(rm)
	if !strings.Contains(got, "1 token") || !strings.Contains(got, "1 IP address") {
		t.Errorf("unexpected summary: %q", got)
	}
	if !strings.HasPrefix(got, "Masked ") || !strings.HasSuffix(got, ".") {
		t.Errorf("summary format wrong: %q", got)
	}
}

func TestSummarizePluralIP(t *testing.T) {
	rm := map[string]string{
		"<IP_1>": "10.0.0.1",
		"<IP_2>": "10.0.0.2",
	}
	got := Summarize(rm)
	if got != "Masked 2 IP addresses." {
		t.Errorf("unexpected summary: %q", got)
	}
}

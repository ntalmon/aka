package suggest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ntalmon/aka/aka-cli/internal/history"
)

// ---- formatDelta ----

func TestFormatDeltaFirstEntryAlwaysStart(t *testing.T) {
	if got := formatDelta(100, 50, 0); got != "[start]" {
		t.Errorf("i==0 should return [start], got %q", got)
	}
}

func TestFormatDeltaZeroCurrentTimestamp(t *testing.T) {
	if got := formatDelta(0, 100, 1); got != "[start]" {
		t.Errorf("zero ts should return [start], got %q", got)
	}
}

func TestFormatDeltaZeroPrevTimestamp(t *testing.T) {
	if got := formatDelta(100, 0, 1); got != "[start]" {
		t.Errorf("zero prevTS should return [start], got %q", got)
	}
}

func TestFormatDeltaSeconds(t *testing.T) {
	if got := formatDelta(1005, 1000, 1); got != "[+5s]" {
		t.Errorf("expected [+5s], got %q", got)
	}
}

func TestFormatDeltaMinuteBoundary(t *testing.T) {
	if got := formatDelta(1060, 1000, 1); got != "[+1m]" {
		t.Errorf("expected [+1m] at 60s, got %q", got)
	}
}

func TestFormatDeltaMinutes(t *testing.T) {
	if got := formatDelta(1000+90, 1000, 1); got != "[+1m]" {
		t.Errorf("expected [+1m], got %q", got)
	}
}

func TestFormatDeltaHours(t *testing.T) {
	if got := formatDelta(1000+7200, 1000, 1); got != "[+2h]" {
		t.Errorf("expected [+2h], got %q", got)
	}
}

func TestFormatDeltaDays(t *testing.T) {
	if got := formatDelta(1000+86400*3, 1000, 1); got != "[+3d]" {
		t.Errorf("expected [+3d], got %q", got)
	}
}

func TestFormatDeltaNegativeClampsToZeroSeconds(t *testing.T) {
	// Out-of-order timestamps: diff is clamped to 0.
	if got := formatDelta(50, 100, 1); got != "[+0s]" {
		t.Errorf("negative diff should clamp to [+0s], got %q", got)
	}
}

// ---- BuildPrompt ----

func TestBuildPromptContainsCommands(t *testing.T) {
	entries := []history.Entry{
		{Command: "git status"},
		{Command: "go build ./..."},
	}
	prompt := BuildPrompt(entries)
	if !strings.Contains(prompt, "git status") {
		t.Error("prompt missing 'git status'")
	}
	if !strings.Contains(prompt, "go build ./...") {
		t.Error("prompt missing 'go build ./...'")
	}
}

func TestBuildPromptNoTimestampsOmitsDeltas(t *testing.T) {
	entries := []history.Entry{
		{Command: "git status"},
		{Command: "go build ./..."},
	}
	prompt := BuildPrompt(entries)
	if strings.Contains(prompt, "[+") || strings.Contains(prompt, "[start]") {
		t.Error("prompt should not contain time deltas when all timestamps are zero")
	}
}

func TestBuildPromptWithTimestampsIncludesDeltas(t *testing.T) {
	entries := []history.Entry{
		{Timestamp: 1000, Command: "git status"},
		{Timestamp: 1005, Command: "git add ."},
		{Timestamp: 2000, Command: "git commit -m 'wip'"},
	}
	prompt := BuildPrompt(entries)
	if !strings.Contains(prompt, "[start]") {
		t.Error("prompt missing [start] for first entry")
	}
	if !strings.Contains(prompt, "[+5s]") {
		t.Error("prompt missing [+5s] delta")
	}
}

func TestBuildPromptWithTimestampsAddsWorkflowHint(t *testing.T) {
	entries := []history.Entry{
		{Timestamp: 1000, Command: "git status"},
		{Timestamp: 1005, Command: "git add ."},
	}
	prompt := BuildPrompt(entries)
	if !strings.Contains(prompt, "workflow session") {
		t.Error("prompt missing workflow session hint when timestamps present")
	}
}

func TestBuildPromptNumbersEntries(t *testing.T) {
	entries := []history.Entry{
		{Command: "git status"},
		{Command: "go build ./..."},
		{Command: "docker ps"},
	}
	prompt := BuildPrompt(entries)
	if !strings.Contains(prompt, "  1.") && !strings.Contains(prompt, "1.") {
		t.Error("prompt missing entry numbering")
	}
	if !strings.Contains(prompt, "3.") {
		t.Error("prompt missing entry 3")
	}
}

func TestBuildPromptNilInput(t *testing.T) {
	prompt := BuildPrompt(nil)
	if prompt == "" {
		t.Error("expected non-empty prompt even for nil input")
	}
}

// ---- ToolSchema ----

func TestToolSchemaReturnsValidJSON(t *testing.T) {
	schema, err := ToolSchema()
	if err != nil {
		t.Fatalf("ToolSchema error: %v", err)
	}
	var v interface{}
	if err := json.Unmarshal(schema, &v); err != nil {
		t.Fatalf("ToolSchema returned invalid JSON: %v", err)
	}
}

func TestToolSchemaHasSuggestionsProperty(t *testing.T) {
	schema, _ := ToolSchema()
	var obj map[string]interface{}
	if err := json.Unmarshal(schema, &obj); err != nil {
		t.Fatal(err)
	}
	props, ok := obj["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("schema missing 'properties'")
	}
	if _, ok := props["suggestions"]; !ok {
		t.Error("schema missing 'suggestions' property")
	}
}

func TestToolSchemaSuggestionsIsRequired(t *testing.T) {
	schema, _ := ToolSchema()
	var obj map[string]interface{}
	json.Unmarshal(schema, &obj)

	required, ok := obj["required"].([]interface{})
	if !ok {
		t.Fatal("schema missing 'required' array")
	}
	for _, r := range required {
		if r == "suggestions" {
			return
		}
	}
	t.Error("'suggestions' not listed in required")
}

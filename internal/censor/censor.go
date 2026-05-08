// Package censor implements two-pass censoring of shell commands:
// Pass 1: secret detection (regex + entropy)
// Pass 2: variable parameterization (structural clustering)
package censor

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/ntalmon/aka/aka-cli/internal/history"
)

// ----------------------------------------------------------------------------
// Pass 1 — Secret censoring
// ----------------------------------------------------------------------------

// secretPattern holds a compiled regex and its placeholder label.
type secretPattern struct {
	re    *regexp.Regexp
	label string // e.g. "SECRET", "TOKEN", "PASSWORD"
}

var secretPatterns = []secretPattern{
	// AWS access key IDs.
	{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "TOKEN"},
	// GitHub tokens (gh[ps]_ prefix).
	{regexp.MustCompile(`gh[ps]_[A-Za-z0-9]{36}`), "TOKEN"},
	// OpenAI keys.
	{regexp.MustCompile(`sk-[A-Za-z0-9]{48}`), "TOKEN"},
	// Anthropic keys.
	{regexp.MustCompile(`sk-ant-[A-Za-z0-9-]{95}`), "TOKEN"},
	// JWTs: three base64url segments separated by dots.
	{regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`), "TOKEN"},
	// Bearer tokens (grab the token value after "Bearer ").
	{regexp.MustCompile(`(?i)Bearer\s+([A-Za-z0-9\-._~+/]+=*)`), "TOKEN"},
	// password=VALUE patterns.
	{regexp.MustCompile(`(?i)(password|passwd|pass|pwd)=\S+`), "PASSWORD"},
	// URL credentials: ://user:pass@host.
	{regexp.MustCompile(`://[^:@\s]+:[^@\s]+@`), "URL_CREDS"},
}

// shannonEntropy calculates the Shannon entropy of a string in bits/char.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	freq := make(map[rune]float64)
	for _, c := range s {
		freq[c]++
	}
	n := float64(len([]rune(s)))
	var h float64
	for _, cnt := range freq {
		p := cnt / n
		h -= p * math.Log2(p)
	}
	return h
}

// isLikelySecret returns true if the token looks high-entropy enough to be a secret.
func isLikelySecret(s string) bool {
	if len(s) < 20 {
		return false
	}
	// Must have a mix of char classes to qualify (avoid flagging long paths, etc.).
	hasUpper := strings.ContainsAny(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	hasLower := strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyz")
	hasDigit := strings.ContainsAny(s, "0123456789")
	if !(hasUpper && hasLower && hasDigit) {
		return false
	}
	return shannonEntropy(s) > 4.5
}

// CensorSecrets replaces known secret patterns and high-entropy tokens with
// deterministic <SECRET_n> / <TOKEN_n> / etc. placeholders.
// Same literal value always gets the same placeholder index.
func CensorSecrets(commands []string) (censored []string, redactionMap map[string]string) {
	seen := make(map[string]string) // value → label (first match wins)

	for _, cmd := range commands {
		// Apply regex patterns.
		for _, p := range secretPatterns {
			matches := p.re.FindAllString(cmd, -1)
			for _, m := range matches {
				if _, ok := seen[m]; !ok {
					seen[m] = p.label
				}
			}
		}

		// Entropy heuristic on standalone tokens.
		tokens := strings.FieldsFunc(cmd, func(r rune) bool {
			return unicode.IsSpace(r)
		})
		for _, tok := range tokens {
			// Strip surrounding quotes/punctuation.
			clean := strings.Trim(tok, `"'`)
			if _, ok := seen[clean]; !ok && isLikelySecret(clean) {
				seen[clean] = "SECRET"
			}
		}
	}

	// Sort candidates for deterministic numbering.
	type kv struct{ val, label string }
	kvs := make([]kv, 0, len(seen))
	for val, label := range seen {
		kvs = append(kvs, kv{val, label})
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].val < kvs[j].val })

	// Assign indices per label.
	labelCount := make(map[string]int)
	valueToPlaceholder := make(map[string]string)
	for _, kv := range kvs {
		labelCount[kv.label]++
		placeholder := fmt.Sprintf("<%s_%d>", kv.label, labelCount[kv.label])
		valueToPlaceholder[kv.val] = placeholder
	}

	// Build redaction map (placeholder → original).
	redactionMap = make(map[string]string)
	for val, ph := range valueToPlaceholder {
		redactionMap[ph] = val
	}

	// Second pass: replace in commands.
	censored = make([]string, len(commands))
	for i, cmd := range commands {
		result := cmd
		// Apply regex replacements first.
		for _, p := range secretPatterns {
			result = p.re.ReplaceAllStringFunc(result, func(m string) string {
				if ph, ok := valueToPlaceholder[m]; ok {
					return ph
				}
				return m
			})
		}
		// Apply entropy replacements.
		for val, ph := range valueToPlaceholder {
			result = strings.ReplaceAll(result, val, ph)
		}
		censored[i] = result
	}
	return censored, redactionMap
}

// ----------------------------------------------------------------------------
// Pass 2 — Variable parameterization
// ----------------------------------------------------------------------------

// commandShape represents the structural shape of a command.
type commandShape struct {
	binary string
	tokens []token
}

type tokenKind int

const (
	tokenFlag       tokenKind = iota // -flag or --flag
	tokenPositional                  // a value position
)

type token struct {
	kind  tokenKind
	value string // for flags; empty for positional slots
}

// parseShape tokenizes a command into its structural shape.
func parseShape(cmd string) commandShape {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return commandShape{}
	}
	shape := commandShape{binary: parts[0]}
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "-") {
			shape.tokens = append(shape.tokens, token{kind: tokenFlag, value: p})
		} else {
			shape.tokens = append(shape.tokens, token{kind: tokenPositional, value: p})
		}
	}
	return shape
}

// shapeKey returns a canonical string key for a command shape (binary + flag sequence).
func shapeKey(s commandShape) string {
	var parts []string
	parts = append(parts, s.binary)
	for _, t := range s.tokens {
		if t.kind == tokenFlag {
			parts = append(parts, t.value)
		} else {
			parts = append(parts, "<POS>")
		}
	}
	return strings.Join(parts, " ")
}

// inferVarType guesses the typed placeholder based on value and context.
func inferVarType(value, binary string, idx int) string {
	v := strings.ToLower(value)
	// Path-like: contains / or starts with ~ or .
	if strings.ContainsAny(value, "/") || strings.HasPrefix(value, "~") || strings.HasPrefix(value, ".") {
		return "PATH"
	}
	// Hostname-like: contains dots but no slashes, looks like a domain.
	if strings.Contains(v, ".") && !strings.Contains(v, "/") && !strings.HasPrefix(v, "<") {
		return "HOST"
	}
	// Branch-like: in git context.
	if binary == "git" {
		return "BRANCH"
	}
	_ = v
	_ = idx
	return "VAR"
}

// ParameterizeVars clusters commands by structural shape and replaces varying
// positional slots with typed <VAR_n> placeholders.
func ParameterizeVars(commands []string) (parameterized []string, varMap map[string]string) {
	// Cluster commands by shape key.
	type cluster struct {
		indices   []int      // indices into commands
		posValues [][]string // posValues[slotIdx] = list of distinct values
		numSlots  int
	}

	clusterMap := make(map[string]*cluster)
	shapeOf := make([]commandShape, len(commands))

	for i, cmd := range commands {
		s := parseShape(cmd)
		shapeOf[i] = s
		key := shapeKey(s)
		if _, ok := clusterMap[key]; !ok {
			// Count positional slots.
			numPos := 0
			for _, t := range s.tokens {
				if t.kind == tokenPositional {
					numPos++
				}
			}
			clusterMap[key] = &cluster{
				numSlots:  numPos,
				posValues: make([][]string, numPos),
			}
		}
		c := clusterMap[key]
		c.indices = append(c.indices, i)

		// Collect positional values.
		posIdx := 0
		for _, t := range s.tokens {
			if t.kind == tokenPositional {
				// Collect distinct values.
				found := false
				for _, existing := range c.posValues[posIdx] {
					if existing == t.value {
						found = true
						break
					}
				}
				if !found {
					c.posValues[posIdx] = append(c.posValues[posIdx], t.value)
				}
				posIdx++
			}
		}
	}

	// Collect all (shapeKey, slotIdx, distinctValues) triples that need vars.
	type slotInfo struct {
		sk      string
		slotIdx int
		values  []string
		binary  string
	}
	var slots []slotInfo
	for sk, c := range clusterMap {
		binary := strings.SplitN(sk, " ", 2)[0]
		for si, vals := range c.posValues {
			if len(vals) >= 2 {
				// Sort values for determinism.
				sorted := make([]string, len(vals))
				copy(sorted, vals)
				sort.Strings(sorted)
				slots = append(slots, slotInfo{sk: sk, slotIdx: si, values: sorted, binary: binary})
			}
		}
	}
	// Sort slots for deterministic numbering.
	sort.Slice(slots, func(i, j int) bool {
		if slots[i].sk != slots[j].sk {
			return slots[i].sk < slots[j].sk
		}
		return slots[i].slotIdx < slots[j].slotIdx
	})

	// Assign var indices.
	typeCount := make(map[string]int)
	slotToVar := make(map[string]map[int]string) // sk → slotIdx → placeholder
	varMap = make(map[string]string)

	for _, slot := range slots {
		// Pick type from first value in the slot.
		typ := inferVarType(slot.values[0], slot.binary, slot.slotIdx)
		if typ == "PATH" {
			// Paths aren't sensitive — leave the real values so the LLM sees actual context.
			continue
		}
		typeCount[typ]++
		ph := fmt.Sprintf("<%s_%d>", typ, typeCount[typ])

		if slotToVar[slot.sk] == nil {
			slotToVar[slot.sk] = make(map[int]string)
		}
		slotToVar[slot.sk][slot.slotIdx] = ph

		// varMap: placeholder → comma-joined example values.
		varMap[ph] = strings.Join(slot.values, ", ")
	}

	// Rewrite commands.
	parameterized = make([]string, len(commands))
	for i, cmd := range commands {
		s := shapeOf[i]
		sk := shapeKey(s)
		slotVars, hasVars := slotToVar[sk]
		if !hasVars {
			parameterized[i] = cmd
			continue
		}

		// Rebuild command with replacements.
		parts := []string{s.binary}
		posIdx := 0
		for _, t := range s.tokens {
			if t.kind == tokenFlag {
				parts = append(parts, t.value)
			} else {
				if ph, ok := slotVars[posIdx]; ok {
					parts = append(parts, ph)
				} else {
					parts = append(parts, t.value)
				}
				posIdx++
			}
		}
		parameterized[i] = strings.Join(parts, " ")
	}
	return parameterized, varMap
}

// CensorAll runs both passes sequentially on the command text of each entry,
// preserving the original timestamps. The internal []string passes are unchanged.
func CensorAll(entries []history.Entry) (censored []history.Entry, redactionMap map[string]string) {
	cmds := make([]string, len(entries))
	for i, e := range entries {
		cmds[i] = e.Command
	}

	pass1, rm1 := CensorSecrets(cmds)
	pass2, rm2 := ParameterizeVars(pass1)

	// Re-attach timestamps.
	censored = make([]history.Entry, len(entries))
	for i, cmd := range pass2 {
		censored[i] = history.Entry{Timestamp: entries[i].Timestamp, Command: cmd}
	}

	// Merge redaction maps.
	merged := make(map[string]string, len(rm1)+len(rm2))
	for k, v := range rm1 {
		merged[k] = v
	}
	for k, v := range rm2 {
		merged[k] = v
	}
	return censored, merged
}

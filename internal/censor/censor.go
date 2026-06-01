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
	re           *regexp.Regexp
	label        string // e.g. "SECRET", "TOKEN", "PASSWORD"
	captureGroup int    // 0 = replace full match; >0 = replace only that submatch group
}

var secretPatterns = []secretPattern{
	// AWS access key IDs.
	{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "TOKEN", 0},
	// GitHub classic tokens (ghp_/ghs_ prefix).
	{regexp.MustCompile(`gh[ps]_[A-Za-z0-9]{36}`), "TOKEN", 0},
	// GitHub fine-grained PATs.
	{regexp.MustCompile(`github_pat_[A-Za-z0-9_]{82}`), "TOKEN", 0},
	// OpenAI legacy keys.
	{regexp.MustCompile(`sk-[A-Za-z0-9]{48}`), "TOKEN", 0},
	// OpenAI project keys.
	{regexp.MustCompile(`sk-proj-[A-Za-z0-9_\-]{20,}`), "TOKEN", 0},
	// Anthropic keys.
	{regexp.MustCompile(`sk-ant-[A-Za-z0-9-]{95}`), "TOKEN", 0},
	// Groq API keys.
	{regexp.MustCompile(`gsk_[A-Za-z0-9]{52}`), "TOKEN", 0},
	// Slack API tokens.
	{regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]+`), "TOKEN", 0},
	// Stripe live secret/publishable keys.
	{regexp.MustCompile(`sk_live_[A-Za-z0-9]{24}`), "TOKEN", 0},
	{regexp.MustCompile(`pk_live_[A-Za-z0-9]{24}`), "TOKEN", 0},
	// Google Cloud Platform API keys.
	{regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`), "TOKEN", 0},
	// JWTs: three base64url segments separated by dots.
	{regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`), "TOKEN", 0},
	// Bearer tokens (grab the token value after "Bearer ").
	{regexp.MustCompile(`(?i)Bearer\s+([A-Za-z0-9\-._~+/]+=*)`), "TOKEN", 0},
	// password=VALUE patterns.
	{regexp.MustCompile(`(?i)(password|passwd|pass|pwd)=\S+`), "PASSWORD", 0},
	// URL credentials: ://user:pass@host.
	{regexp.MustCompile(`://[^:@\s]+:[^@\s]+@`), "URL_CREDS", 0},
	// Full git commit SHA-1 hashes (40 lowercase hex chars).
	{regexp.MustCompile(`\b[0-9a-f]{40}\b`), "COMMIT_HASH", 0},
	// Git commit message: -m "..." — replace only the message text (group 2), keeping -m "...".
	{regexp.MustCompile(`(-m\s+")([^"]*)(")`), "COMMIT_MSG", 2},
	// Git commit message: -m '...'
	{regexp.MustCompile(`(-m\s+')([^']*)(')`), "COMMIT_MSG", 2},
	// Git commit message: --message=value
	{regexp.MustCompile(`(--message=)(\S+)`), "COMMIT_MSG", 2},
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
	// Require at least 2 of 3 char classes to qualify (avoids flagging long paths,
	// all-lowercase prose, etc., while catching hex/base64 secrets that lack one class).
	hasUpper := strings.ContainsAny(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	hasLower := strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyz")
	hasDigit := strings.ContainsAny(s, "0123456789")
	charClasses := 0
	if hasUpper {
		charClasses++
	}
	if hasLower {
		charClasses++
	}
	if hasDigit {
		charClasses++
	}
	if charClasses < 2 {
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
			if p.captureGroup == 0 {
				for _, m := range p.re.FindAllString(cmd, -1) {
					if _, ok := seen[m]; !ok {
						seen[m] = p.label
					}
				}
			} else {
				for _, m := range p.re.FindAllStringSubmatch(cmd, -1) {
					if p.captureGroup < len(m) && m[p.captureGroup] != "" {
						if _, ok := seen[m[p.captureGroup]]; !ok {
							seen[m[p.captureGroup]] = p.label
						}
					}
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
			if p.captureGroup == 0 {
				result = p.re.ReplaceAllStringFunc(result, func(m string) string {
					if ph, ok := valueToPlaceholder[m]; ok {
						return ph
					}
					return m
				})
			} else {
				result = replaceSubmatch(result, p.re, p.captureGroup, valueToPlaceholder)
			}
		}
		// Apply entropy replacements.
		for val, ph := range valueToPlaceholder {
			result = strings.ReplaceAll(result, val, ph)
		}
		censored[i] = result
	}
	return censored, redactionMap
}

// replaceSubmatch replaces only the specified capture group within each match,
// leaving the rest of the match (e.g., surrounding quotes or flag prefix) intact.
func replaceSubmatch(s string, re *regexp.Regexp, group int, replacements map[string]string) string {
	indices := re.FindAllStringSubmatchIndex(s, -1)
	if len(indices) == 0 {
		return s
	}
	var b strings.Builder
	prev := 0
	for _, idx := range indices {
		gStart, gEnd := idx[2*group], idx[2*group+1]
		if gStart < 0 {
			continue
		}
		b.WriteString(s[prev:gStart])
		val := s[gStart:gEnd]
		if ph, ok := replacements[val]; ok {
			b.WriteString(ph)
		} else {
			b.WriteString(val)
		}
		prev = gEnd
	}
	b.WriteString(s[prev:])
	return b.String()
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

// isIPv4 returns true if s looks like an IPv4 address (four decimal octets).
func isIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 3 {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// isPortNumber returns true if s is a pure integer in the valid port range [1, 65535].
func isPortNumber(s string) bool {
	if len(s) == 0 || len(s) > 5 {
		return false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n >= 1 && n <= 65535
}

// anyHasDigit reports whether any string in ss contains an ASCII digit.
func anyHasDigit(ss []string) bool {
	for _, s := range ss {
		for _, c := range s {
			if c >= '0' && c <= '9' {
				return true
			}
		}
	}
	return false
}

// inferVarType guesses the typed placeholder based on value.
func inferVarType(value string, idx int) string {
	v := strings.ToLower(value)
	// Path-like: contains / or starts with ~ or .
	if strings.ContainsAny(value, "/") || strings.HasPrefix(value, "~") || strings.HasPrefix(value, ".") {
		return "PATH"
	}
	// IPv4 address: censor (may reveal internal network topology).
	if isIPv4(value) {
		return "IP"
	}
	// Port number: not sensitive, leave as-is.
	if isPortNumber(value) {
		return "PORT"
	}
	// Hostname-like: contains dots but no slashes, looks like a domain.
	if strings.Contains(v, ".") && !strings.Contains(v, "/") && !strings.HasPrefix(v, "<") {
		return "HOST"
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
			if len(vals) == 0 {
				continue
			}
			peekTyp := inferVarType(vals[0], si)
			// Ports and paths are never parameterized.
			if peekTyp == "PATH" || peekTyp == "PORT" {
				continue
			}
			// Git positional args (branch names, remotes, tags, refs) are not
			// parameterized — only IPs are, as they may reveal network topology.
			if binary == "git" && peekTyp != "IP" {
				continue
			}
			// VAR-typed slots with no digit in any value are treated as subcommand
			// identifiers (e.g. "scan", "set-model"), not varying data. Real data
			// (server IDs, versions, filenames) almost always contains a digit.
			if peekTyp == "VAR" && !anyHasDigit(vals) {
				continue
			}
			// IPs are always parameterized (even a single distinct value).
			// All other types require ≥2 distinct values.
			if peekTyp != "IP" && len(vals) < 2 {
				continue
			}
			sorted := make([]string, len(vals))
			copy(sorted, vals)
			sort.Strings(sorted)
			slots = append(slots, slotInfo{sk: sk, slotIdx: si, values: sorted, binary: binary})
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
		typ := inferVarType(slot.values[0], slot.slotIdx)
		if typ == "PATH" || typ == "PORT" {
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

// labelFriendly maps a redaction placeholder label to a human-readable plural noun.
func labelFriendly(label string) string {
	switch label {
	case "TOKEN":
		return "token"
	case "SECRET":
		return "secret"
	case "PASSWORD":
		return "password"
	case "URL_CREDS":
		return "URL credential"
	case "COMMIT_HASH":
		return "commit hash"
	case "COMMIT_MSG":
		return "commit message"
	case "IP":
		return "IP address"
	case "HOST":
		return "hostname"
	case "VAR":
		return "variable"
	default:
		return strings.ToLower(label)
	}
}

// Summarize turns a redaction map (placeholder → original) into a human
// readable string like "Masked 2 tokens, 1 IP address." When the map is empty
// it returns "No sensitive data detected."
func Summarize(redactionMap map[string]string) string {
	if len(redactionMap) == 0 {
		return "No sensitive data detected."
	}

	// Count occurrences per label by parsing placeholder keys like <TOKEN_1>.
	counts := make(map[string]int)
	for ph := range redactionMap {
		// Strip < and > then split on _
		inner := strings.TrimSuffix(strings.TrimPrefix(ph, "<"), ">")
		parts := strings.SplitN(inner, "_", 2)
		if len(parts) > 0 {
			counts[parts[0]]++
		}
	}

	// Emit in deterministic order matching labelFriendly's switch cases.
	order := []string{"TOKEN", "SECRET", "PASSWORD", "URL_CREDS", "COMMIT_HASH", "COMMIT_MSG", "IP", "HOST", "VAR"}
	var parts []string
	for _, label := range order {
		n := counts[label]
		if n == 0 {
			continue
		}
		noun := labelFriendly(label)
		if n != 1 {
			// Simple pluralisation for known nouns.
			switch noun {
			case "IP address":
				noun = "IP addresses"
			case "hostname":
				noun = "hostnames"
			default:
				noun += "s"
			}
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, noun))
	}
	// Include any unknown labels not in the ordered list.
	for label, n := range counts {
		found := false
		for _, o := range order {
			if o == label {
				found = true
				break
			}
		}
		if !found {
			noun := labelFriendly(label)
			if n != 1 {
				noun += "s"
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, noun))
		}
	}

	return "Masked " + strings.Join(parts, ", ") + "."
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

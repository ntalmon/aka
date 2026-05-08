package censor

import (
	"strings"
	"testing"
)

func TestCensorSecretsAWSKey(t *testing.T) {
	// Real AWS key format: AKIA + exactly 16 uppercase alphanumeric chars.
	awsKey := "AKIAIOSFODNN7EXAMPLE"
	cmds := []string{"aws s3 ls --access-key " + awsKey}
	censored, rm := CensorSecrets(cmds)
	if strings.Contains(censored[0], awsKey) {
		t.Errorf("AWS key not censored: %q", censored[0])
	}
	if len(rm) == 0 {
		t.Error("redactionMap empty after AWS key censor")
	}
}

func TestCensorSecretsGitHubToken(t *testing.T) {
	token := "ghp_abcdefghijklmnopqrstuvwxyzABCDEFGHIJ"
	cmds := []string{"curl -H 'Authorization: token " + token + "' https://api.github.com"}
	censored, _ := CensorSecrets(cmds)
	if strings.Contains(censored[0], token) {
		t.Errorf("GitHub token not censored: %q", censored[0])
	}
}

func TestCensorSecretsJWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	cmds := []string{"curl -H 'Authorization: Bearer " + jwt + "' https://api.example.com"}
	censored, _ := CensorSecrets(cmds)
	if strings.Contains(censored[0], jwt) {
		t.Errorf("JWT not censored: %q", censored[0])
	}
}

func TestCensorSecretsPasswordParam(t *testing.T) {
	cmds := []string{"psql postgres://localhost/mydb password=mysecretpass123"}
	censored, _ := CensorSecrets(cmds)
	if strings.Contains(censored[0], "mysecretpass123") {
		t.Errorf("password not censored: %q", censored[0])
	}
}

func TestCensorSecretsURLCredentials(t *testing.T) {
	cmds := []string{"psql postgres://admin:s3cr3tP@ssw0rd@db.example.com/mydb"}
	censored, _ := CensorSecrets(cmds)
	if strings.Contains(censored[0], "s3cr3tP@ssw0rd") {
		t.Errorf("URL credentials not censored: %q", censored[0])
	}
}

func TestCensorSecretsSameValueSamePlaceholder(t *testing.T) {
	// Real AWS key format: AKIA + exactly 16 uppercase alphanumeric chars.
	key := "AKIAIOSFODNN7EXAMPLE"
	cmds := []string{
		"aws s3 ls --key " + key,
		"aws ec2 describe --key " + key,
	}
	censored, rm := CensorSecrets(cmds)
	// Both should have the same placeholder.
	ph := ""
	for p, v := range rm {
		if v == key {
			ph = p
		}
	}
	if ph == "" {
		t.Fatalf("no placeholder found for key %q in rm=%v", key, rm)
	}
	if !strings.Contains(censored[0], ph) {
		t.Errorf("first command missing placeholder %q: %q", ph, censored[0])
	}
	if !strings.Contains(censored[1], ph) {
		t.Errorf("second command missing placeholder %q: %q", ph, censored[1])
	}
}

func TestCensorSecretsDontRedactNormalStrings(t *testing.T) {
	// Short strings, commit hashes, branch names — should NOT be redacted.
	cmds := []string{
		"git checkout main",
		"git commit -m 'fix: update readme'",
		"git log --oneline -10",
	}
	censored, rm := CensorSecrets(cmds)
	for i, c := range censored {
		if c != cmds[i] {
			t.Errorf("normal command was altered: original=%q censored=%q (rm=%v)", cmds[i], c, rm)
		}
	}
}

func TestCensorSecretsOpenAIKey(t *testing.T) {
	// sk- followed by 48 alphanumeric chars.
	key := "sk-" + strings.Repeat("abcABC1", 6) + strings.Repeat("23456789", 3) // 48 alphanum
	cmds := []string{"curl -H 'Authorization: Bearer " + key + "' https://api.openai.com/v1/completions"}
	censored, _ := CensorSecrets(cmds)
	if strings.Contains(censored[0], key) {
		t.Errorf("OpenAI key not censored: %q", censored[0])
	}
}

func TestCensorSecretsDeterministic(t *testing.T) {
	cmds := []string{
		"aws s3 ls --key AKIAIOSFODNN7EXAMPLE",
		"gh api repos --token ghp_abcdefghijklmnopqrstuvwxyzABCDEFGHIJ",
	}
	c1, rm1 := CensorSecrets(cmds)
	c2, rm2 := CensorSecrets(cmds)
	for i := range c1 {
		if c1[i] != c2[i] {
			t.Errorf("non-deterministic censor at index %d", i)
		}
	}
	for k := range rm1 {
		if rm1[k] != rm2[k] {
			t.Errorf("non-deterministic redactionMap key %q", k)
		}
	}
}

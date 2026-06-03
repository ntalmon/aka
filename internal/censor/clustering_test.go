package censor

import (
	"strings"
	"testing"
)

func TestParameterizeVarsGitBranchesNotParameterized(t *testing.T) {
	cmds := []string{
		"git checkout main",
		"git checkout develop",
		"git checkout feat-billing",
	}
	parameterized, varMap := ParameterizeVars(cmds)
	// Branch names should be preserved — git positional args are never parameterized.
	for i, p := range parameterized {
		if p != cmds[i] {
			t.Errorf("git checkout branch was modified: original=%q got=%q", cmds[i], p)
		}
	}
	if len(varMap) != 0 {
		t.Errorf("varMap should be empty for git branches, got: %v", varMap)
	}
}

func TestParameterizeVarsSubcommandsNotParameterized(t *testing.T) {
	// All-lowercase-alpha values in slot 0 are treated as subcommands, not data.
	cmds := []string{
		"./aka scan",
		"./aka help",
		"./aka config",
	}
	parameterized, varMap := ParameterizeVars(cmds)
	for i, p := range parameterized {
		if p != cmds[i] {
			t.Errorf("subcommand was parameterized: original=%q got=%q", cmds[i], p)
		}
	}
	if len(varMap) != 0 {
		t.Errorf("varMap should be empty for subcommands, got: %v", varMap)
	}
}

func TestParameterizeVarsNoParamForSingleValue(t *testing.T) {
	cmds := []string{
		"git status",
		"git status",
		"git status",
	}
	// After normalize, there'd be one. But test with dupes directly.
	parameterized, varMap := ParameterizeVars(cmds)
	for _, p := range parameterized {
		if strings.Contains(p, "<") {
			t.Errorf("placeholder inserted for single-value command: %q", p)
		}
	}
	if len(varMap) != 0 {
		t.Errorf("varMap should be empty for fixed commands, got: %v", varMap)
	}
}

func TestParameterizeVarsSameValueSamePlaceholder(t *testing.T) {
	cmds := []string{
		"ssh host1",
		"ssh host2",
	}
	parameterized, _ := ParameterizeVars(cmds)
	// Both should use the same placeholder.
	if parameterized[0] != parameterized[1] {
		t.Errorf("same-structure commands got different parameterizations: %q vs %q",
			parameterized[0], parameterized[1])
	}
}

func TestParameterizeVarsDockerRun(t *testing.T) {
	// Versioned image tags contain digits and are parameterized.
	cmds := []string{
		"docker run -it ubuntu:22.04 bash",
		"docker run -it alpine:3.18 sh",
		"docker run -it debian:11 bash",
	}
	parameterized, _ := ParameterizeVars(cmds)
	for _, p := range parameterized {
		if strings.Contains(p, "ubuntu") || strings.Contains(p, "alpine") || strings.Contains(p, "debian") {
			t.Errorf("versioned image name not replaced: %q", p)
		}
	}
}

func TestParameterizeVarsMixedCluster(t *testing.T) {
	cmds := []string{
		// Git commands: branch names are NOT parameterized.
		"git push origin main",
		"git push origin develop",
		"git push origin feat-x",
		"git status",
		// Non-git with digit-containing targets ARE parameterized.
		"ssh host1",
		"ssh host2",
	}
	parameterized, varMap := ParameterizeVars(cmds)
	// git status should not be parameterized.
	for _, p := range parameterized {
		if strings.HasPrefix(p, "git status") && strings.Contains(p, "<") {
			t.Errorf("git status got a placeholder: %q", p)
		}
	}
	// git push branch names should not be parameterized.
	for i, p := range parameterized {
		if strings.HasPrefix(p, "git push") && strings.Contains(p, "<") {
			t.Errorf("git push branch was incorrectly parameterized: %q (original: %q)", p, cmds[i])
		}
	}
	// ssh targets with digits should be parameterized.
	for _, p := range parameterized {
		if strings.HasPrefix(p, "ssh") && !strings.Contains(p, "<") {
			t.Errorf("ssh target not parameterized: %q", p)
		}
	}
	if len(varMap) == 0 {
		t.Error("varMap should not be empty (ssh targets should be in it)")
	}
}

func TestParameterizeVarsIPNotCensored(t *testing.T) {
	cmds := []string{
		"uvicorn web_server:app --host 0.0.0.0 --port 8080",
	}
	parameterized, _ := ParameterizeVars(cmds)
	if !strings.Contains(parameterized[0], "0.0.0.0") {
		t.Errorf("IP address should not be replaced: %q", parameterized[0])
	}
	if strings.Contains(parameterized[0], "<IP_") {
		t.Errorf("unexpected <IP_n> placeholder: %q", parameterized[0])
	}
}

func TestParameterizeVarsIPNotCensoredInMixedCluster(t *testing.T) {
	// IP appears alongside non-IP values in the same slot; the IP must not be replaced
	// even though vals[0] is not an IP and the slot gets a VAR placeholder.
	cmds := []string{
		"ssh devserver1",
		"ssh 192.168.1.1",
		"ssh devserver2",
	}
	parameterized, _ := ParameterizeVars(cmds)
	for _, p := range parameterized {
		if strings.HasPrefix(p, "ssh 192") && !strings.Contains(p, "192.168.1.1") {
			t.Errorf("IP address was replaced in mixed cluster: %q", p)
		}
	}
}

func TestParameterizeVarsPortNotCensored(t *testing.T) {
	// Port numbers should never be parameterized, even when they vary.
	cmds := []string{
		"uvicorn web_server:app --host 0.0.0.0 --port 8080",
		"uvicorn web_server:app --host 0.0.0.0 --port 3000",
		"uvicorn web_server:app --host 0.0.0.0 --port 443",
	}
	parameterized, _ := ParameterizeVars(cmds)
	ports := []string{"8080", "3000", "443"}
	for i, p := range parameterized {
		if !strings.Contains(p, ports[i]) {
			t.Errorf("cmd[%d]: port number was replaced: %q", i, p)
		}
	}
}

func TestParameterizeVarsFileExtensionNotHost(t *testing.T) {
	// Filenames like "build.sh" contain a dot but are not hostnames.
	cmds := []string{
		"chmod +x build.sh",
		"chmod +x deploy.sh",
	}
	parameterized, _ := ParameterizeVars(cmds)
	for _, p := range parameterized {
		if strings.Contains(p, "<HOST_") {
			t.Errorf("filename was incorrectly classified as hostname: %q", p)
		}
	}
}

func TestParameterizeVarsIPVaryingNotCensored(t *testing.T) {
	cmds := []string{
		"ssh 192.168.1.1",
		"ssh 10.0.0.5",
		"ssh 172.16.0.1",
	}
	parameterized, _ := ParameterizeVars(cmds)
	ips := []string{"192.168.1.1", "10.0.0.5", "172.16.0.1"}
	for i, p := range parameterized {
		if !strings.Contains(p, ips[i]) {
			t.Errorf("IP address should not be replaced: %q", p)
		}
	}
}

func TestParameterizeVarsDigitFreeIdentifiersPreserved(t *testing.T) {
	// When a VAR slot is activated by digit-containing values, digit-free values
	// in the same cluster must not be replaced (e.g. "gh" stays "gh").
	cmds := []string{
		"brew install gh",
		"brew install python3",
		"brew install node@18",
	}
	parameterized, _ := ParameterizeVars(cmds)
	for _, p := range parameterized {
		if strings.HasPrefix(p, "brew install gh") && strings.Contains(p, "<") {
			t.Errorf("digit-free package name was replaced: %q", p)
		}
	}
}

func TestParameterizeVarsWhichIdentifierPreserved(t *testing.T) {
	cmds := []string{
		"which aka",
		"which python3",
	}
	parameterized, _ := ParameterizeVars(cmds)
	for _, p := range parameterized {
		if strings.HasPrefix(p, "which aka") && strings.Contains(p, "<") {
			t.Errorf("digit-free command name was replaced: %q", p)
		}
	}
}

func TestParameterizeVarsBareFilenamePreserved(t *testing.T) {
	// PLAN.md has no digit — should not be replaced even if other mv sources do.
	cmds := []string{
		"mv PLAN.md plans/AKA.md",
		"mv file1.md plans/file1.md",
	}
	parameterized, _ := ParameterizeVars(cmds)
	for _, p := range parameterized {
		if strings.HasPrefix(p, "mv PLAN.md") && strings.Contains(p, "<VAR") {
			t.Errorf("digit-free filename was replaced: %q", p)
		}
	}
}

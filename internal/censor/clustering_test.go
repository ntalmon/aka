package censor

import (
	"strings"
	"testing"
)

func TestParameterizeVarsGitCheckout(t *testing.T) {
	cmds := []string{
		"git checkout main",
		"git checkout develop",
		"git checkout feat-billing",
	}
	parameterized, varMap := ParameterizeVars(cmds)
	// All three should become "git checkout <BRANCH_1>" or similar.
	for _, p := range parameterized {
		if strings.Contains(p, "main") || strings.Contains(p, "develop") || strings.Contains(p, "feat-billing") {
			t.Errorf("value not replaced: %q", p)
		}
		if !strings.Contains(p, "<") {
			t.Errorf("no placeholder inserted: %q", p)
		}
	}
	if len(varMap) == 0 {
		t.Error("varMap should not be empty")
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
		"git checkout feat-x",
		"git checkout feat-y",
	}
	parameterized, _ := ParameterizeVars(cmds)
	// Both should use the same placeholder.
	if parameterized[0] != parameterized[1] {
		t.Errorf("same-structure commands got different parameterizations: %q vs %q",
			parameterized[0], parameterized[1])
	}
}

func TestParameterizeVarsDockerRun(t *testing.T) {
	cmds := []string{
		"docker run -it ubuntu bash",
		"docker run -it alpine sh",
		"docker run -it debian bash",
	}
	parameterized, _ := ParameterizeVars(cmds)
	// ubuntu, alpine, debian should become a var; bash/sh might vary too.
	for _, p := range parameterized {
		if strings.Contains(p, "ubuntu") || strings.Contains(p, "alpine") || strings.Contains(p, "debian") {
			t.Errorf("image name not replaced: %q", p)
		}
	}
}

func TestParameterizeVarsMixedCluster(t *testing.T) {
	cmds := []string{
		// Cluster 1: git push origin <branch> — branch varies.
		"git push origin main",
		"git push origin develop",
		"git push origin feat-x",
		// Cluster 2: git status — no params.
		"git status",
	}
	parameterized, varMap := ParameterizeVars(cmds)
	// git status should not be parameterized.
	for _, p := range parameterized {
		if strings.HasPrefix(p, "git status") && strings.Contains(p, "<") {
			t.Errorf("git status got a placeholder: %q", p)
		}
	}
	// The push commands should all use the same placeholder.
	pushCmds := []string{}
	for _, p := range parameterized {
		if strings.HasPrefix(p, "git push") {
			pushCmds = append(pushCmds, p)
		}
	}
	if len(pushCmds) != 3 {
		t.Fatalf("expected 3 push commands, got %d", len(pushCmds))
	}
	if pushCmds[0] != pushCmds[1] || pushCmds[1] != pushCmds[2] {
		t.Errorf("push commands have different parameterizations: %v", pushCmds)
	}
	if len(varMap) == 0 {
		t.Error("varMap should not be empty")
	}
}

func TestParameterizeVarsIPAlwaysCensored(t *testing.T) {
	// IP appears only once but should still be parameterized.
	cmds := []string{
		"uvicorn web_server:app --host 0.0.0.0 --port 8080",
	}
	parameterized, varMap := ParameterizeVars(cmds)
	if strings.Contains(parameterized[0], "0.0.0.0") {
		t.Errorf("IP address not replaced: %q", parameterized[0])
	}
	if !strings.Contains(parameterized[0], "<IP_") {
		t.Errorf("expected <IP_n> placeholder: %q", parameterized[0])
	}
	if len(varMap) == 0 {
		t.Error("varMap should not be empty")
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

func TestParameterizeVarsIPVaryingCensored(t *testing.T) {
	// Multiple distinct IPs should all map to the same placeholder.
	cmds := []string{
		"ssh 192.168.1.1",
		"ssh 10.0.0.5",
		"ssh 172.16.0.1",
	}
	parameterized, _ := ParameterizeVars(cmds)
	for _, p := range parameterized {
		if !strings.Contains(p, "<IP_") {
			t.Errorf("expected <IP_n> placeholder: %q", p)
		}
	}
	if parameterized[0] != parameterized[1] || parameterized[1] != parameterized[2] {
		t.Errorf("varying IPs got different parameterizations: %v", parameterized)
	}
}

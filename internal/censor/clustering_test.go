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

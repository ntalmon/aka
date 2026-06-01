package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// detectCurrentShell returns the shell name ("bash" or "zsh") for the current invocation.
// Priority: $AKA_SHELL (set by the wrapper) → parent process name → $SHELL path.
// Parent process detection is used because env vars like $ZSH_VERSION and $BASH_VERSION
// are inherited by child shells and cannot reliably identify the currently running shell.
func detectCurrentShell() string {
	if s := os.Getenv("AKA_SHELL"); s != "" {
		return s
	}
	if name := shellFromParentProcess(); name != "" {
		return name
	}
	return shellNameFromPath(os.Getenv("SHELL"))
}

// shellFromParentProcess identifies the shell by inspecting the parent process name.
func shellFromParentProcess() string {
	ppid := os.Getppid()
	out, err := exec.Command("ps", "-p", strconv.Itoa(ppid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return shellNameFromPath(strings.TrimSpace(string(out)))
}

// shellNameFromPath converts a shell name or path like "/bin/zsh" or "-zsh" to a short name.
func shellNameFromPath(shellPath string) string {
	switch {
	case strings.Contains(shellPath, "zsh"):
		return "zsh"
	case strings.Contains(shellPath, "bash"):
		return "bash"
	default:
		return ""
	}
}

// rcFileForShell returns the RC file path for the given shell.
func rcFileForShell(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	case "bash":
		rc := filepath.Join(home, ".bashrc")
		profile := filepath.Join(home, ".bash_profile")
		if _, err := os.Stat(profile); err == nil {
			if _, err2 := os.Stat(rc); os.IsNotExist(err2) {
				return profile, nil
			}
		}
		return rc, nil
	default:
		return filepath.Join(home, ".bashrc"), nil
	}
}

// isShellInitialized reports whether ~/.config/aka/<shell>/ exists.
func isShellInitialized(shell string) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(home, ".config", "aka", shell))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// requireShellInitialized prints a friendly message and returns false if the shell
// has not been initialized. Callers should return nil when this returns false.
func requireShellInitialized(shell string) (bool, error) {
	if shell == "" {
		return false, nil
	}
	ok, err := isShellInitialized(shell)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return true, nil
}

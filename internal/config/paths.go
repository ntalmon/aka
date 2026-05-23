package config

import "github.com/ntalmon/aka/aka-cli/internal/aliases"

// GetAliasesPath returns the path to the managed aliases file for shell.
func GetAliasesPath(shell string) (string, error) {
	return aliases.AliasesFilePath(shell)
}

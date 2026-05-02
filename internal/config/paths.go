package config

import "github.com/ntalmon/aka/aka-cli/internal/aliases"

// GetAliasesPath returns the path to the managed aliases file.
func GetAliasesPath() (string, error) {
	return aliases.AliasesFilePath()
}

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

var binPath string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "aka-integration-build-*")
	if err != nil {
		fmt.Printf("failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binPath = tmpDir + "/aka-cli"

	// Build the binary
	cmd := exec.Command("go", "build", "-o", binPath, "../cmd/aka")
	if err := cmd.Run(); err != nil {
		fmt.Printf("failed to build binary: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

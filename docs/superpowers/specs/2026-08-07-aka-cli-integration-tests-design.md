# aka-cli Integration Tests Design

## Overview
This document outlines the design for the integration testing suite of `aka-cli`. The goal is to verify the end-to-end functionality of the CLI without using mocks, by executing the compiled binary in an isolated environment and simulating user interactions.

## Architecture

### 1. Test Location
Tests will reside in an `integration/` directory at the project root to keep them distinct from unit tests in `internal/`. This ensures they can be run specifically or collectively using standard `go test ./...` commands.

### 2. Binary Compilation (Test Setup)
To ensure we are testing the actual artifact, the test suite will compile the `aka-cli` binary programmatically once during the test setup phase (e.g., in `TestMain` or a specialized setup function) using `go build`. The resulting executable will be run for each test case.

### 3. Isolated Environment
Each test will run within a fresh temporary directory created via `t.TempDir()`. This isolates the tests from the user's real file system.
Environment variables passed to the test process will include:
- `HOME`: Points to the temp directory.
- `XDG_CONFIG_HOME`: Ensures configuration is written to the temp directory (`<temp_dir>/.config/aka`).
- `SHELL`: Set to a valid shell name (e.g., `zsh`) for the CLI to use.

## Execution & Interactivity

### 1. Process and PTY Emulation
Since `aka-cli` uses interactive TUIs (`bubbletea`, `huh`), standard stdin/stdout pipes are insufficient. The tests will utilize `github.com/Netflix/go-expect`, which leverages pseudo-terminals (PTY) to spawn the CLI process. This tricks the UI libraries into rendering their interactive components correctly.

### 2. Interactions
Tests will define `expect` sequences to interact with the CLI:
- **Expectations**: Waiting for specific TUI text or prompts to appear on the pseudo-terminal.
- **Interactions**: Sending simulated keystrokes (Enter, Down Arrow, text input) to navigate menus and submit data.

### 3. Handling External Dependencies (LLM API)
To test commands like `aka scan` that rely on an external LLM, the tests will inject a dummy API key via environment variables. The test will verify that:
1. The history ingestion and local censoring work correctly.
2. The UI review prompt is displayed.
3. Upon acceptance, the network request gracefully fails due to the dummy key, and the CLI outputs the expected authorization failure message.

## Verification
- Test passes if the sequence of expectations and interactions completes successfully, and the process exits with the expected status code.
- File system side-effects (e.g., the creation of `aliases.sh` or `installed.json`) will be verified in the temporary directory after the command completes.

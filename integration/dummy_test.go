package integration

import "testing"

func TestDummy(t *testing.T) {
	if binPath == "" {
		t.Fatal("binPath should not be empty")
	}
}

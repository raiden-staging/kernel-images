package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Println("skipping e2e tests: docker not found")
		os.Exit(0)
	}
	if _, err := exec.LookPath("pnpm"); err != nil {
		fmt.Println("skipping e2e tests: pnpm not found")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

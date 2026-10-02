package forge

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is not a test: it is the child process that
// TestRunCommandTimesOut starts (the standard os/exec test-helper pattern).
func TestHelperProcess(t *testing.T) {
	switch os.Getenv("WORKSTATION_HELPER") {
	case "sleep":
		time.Sleep(30 * time.Second)
	case "echo":
		os.Stdout.WriteString("hello")
	case "fail":
		os.Stderr.WriteString("boom")
		os.Exit(3)
	}
}

func helperEnv(mode string) []string {
	return append(os.Environ(), "WORKSTATION_HELPER="+mode)
}

func TestRunCommand(t *testing.T) {
	out, err := runCommand(5*time.Second, "", helperEnv("echo"), os.Args[0], "-test.run=TestHelperProcess")
	if err != nil || !strings.Contains(string(out), "hello") {
		t.Fatalf("got %q %v", out, err)
	}

	_, err = runCommand(5*time.Second, "", helperEnv("fail"), os.Args[0], "-test.run=TestHelperProcess")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("stderr must be part of the error, got %v", err)
	}
}

func TestRunCommandTimesOut(t *testing.T) {
	start := time.Now()
	_, err := runCommand(300*time.Millisecond, "", helperEnv("sleep"), os.Args[0], "-test.run=TestHelperProcess")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error, got %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("a hung gh must be killed at the timeout, took %s", took)
	}
}

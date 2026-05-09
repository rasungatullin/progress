package cli

import (
	"testing"

	"github.com/rasungatullin/progress/internal/execution/launch"
	"github.com/spf13/cobra"
)

func TestBindLaunchFlagsAndInvocation(t *testing.T) {
	t.Parallel()

	flags := newLaunchFlags()
	cmd := &cobra.Command{Use: "launch"}
	bindLaunchFlags(cmd, flags)

	err := cmd.ParseFlags([]string{
		"--dir", "/tmp/work",
		"--runner", "opencode",
		"--model", "openai/gpt-5.4",
		"--prompt", "ship it",
		"--commit-push",
		"--commit-message", "Custom message",
	})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	invocation := invocationFromLaunchFlags(flags)
	if invocation.Launch.Directory != "/tmp/work" {
		t.Fatalf("unexpected directory: %q", invocation.Launch.Directory)
	}
	if !invocation.Launch.CommitPush {
		t.Fatal("expected commit-push to be enabled")
	}
	if invocation.Launch.CommitMessage != "Custom message" {
		t.Fatalf("unexpected commit message: %q", invocation.Launch.CommitMessage)
	}
	if invocation.Launch.Prompt != "ship it" {
		t.Fatalf("unexpected prompt: %q", invocation.Launch.Prompt)
	}
}

func TestNewLaunchFlagsDefaults(t *testing.T) {
	t.Parallel()

	flags := newLaunchFlags()
	if flags.commitPush {
		t.Fatal("commit-push must be disabled by default")
	}
	if flags.commitMessage != "" {
		t.Fatalf("commit-message flag value should be empty before binding, got %q", flags.commitMessage)
	}

	cmd := &cobra.Command{Use: "launch"}
	bindLaunchFlags(cmd, flags)
	if err := cmd.ParseFlags([]string{"--dir", "/tmp/work", "--prompt", "task"}); err != nil {
		t.Fatalf("parse default flags: %v", err)
	}

	invocation := invocationFromLaunchFlags(flags)
	if invocation.Launch.CommitPush {
		t.Fatal("commit-push must stay disabled by default")
	}
	if invocation.Launch.CommitMessage != launch.DefaultCommitMessage {
		t.Fatalf("unexpected default commit message: %q", invocation.Launch.CommitMessage)
	}
}

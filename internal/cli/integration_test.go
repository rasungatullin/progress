package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestIntegrationDispatcherCommandPrintsDiagnosticRoute(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "dispatcher", "--system", "github", "--resource", "issue", "--operation", "get"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute dispatcher command: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"provider=github\n",
		"provider-available=false\n",
		"resource=issue\n",
		"operation=get\n",
		"expected-result=tracker-issue\n",
		"diagnostic=request system=github resource=issue operation=get\n",
		"diagnostic=dispatcher mode=diagnostic-only\n",
		"diagnostic=provider=github not registered in current build\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("dispatcher output must include %q, got %q", fragment, output)
		}
	}
}

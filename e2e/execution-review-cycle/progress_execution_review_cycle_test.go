package executionreviewcycle_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProgressExecutionReviewFixCycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("сценарий использует POSIX sh для стендового исполнительного модуля")
	}

	root := findRepoRoot(t)
	tmp := t.TempDir()
	progressBin := filepath.Join(tmp, "progress")
	run(t, root, nil, "go", "build", "-o", progressBin, "./cmd/progress")

	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("create fake bin: %v", err)
	}
	writeFile(t, filepath.Join(fakeBin, "opencode"), fakeOpenCodeScript(), 0o755)

	remote := filepath.Join(tmp, "target.git")
	run(t, tmp, nil, "git", "init", "--bare", "--initial-branch=main", remote)
	seed := filepath.Join(tmp, "seed")
	run(t, tmp, nil, "git", "clone", remote, seed)
	configureGitIdentity(t, seed)
	writeFile(t, filepath.Join(seed, "README.md"), "# E2E target\n", 0o644)
	writeFile(t, filepath.Join(seed, "result.txt"), "base\n", 0o644)
	run(t, seed, nil, "git", "add", "README.md", "result.txt")
	run(t, seed, nil, "git", "commit", "-m", "Подготовить целевой репозиторий")
	run(t, seed, nil, "git", "push", "origin", "main")
	run(t, seed, nil, "git", "switch", "-c", "101")
	run(t, seed, nil, "git", "push", "origin", "101")
	run(t, seed, nil, "git", "switch", "main")

	repo := filepath.Join(tmp, "repo")
	run(t, tmp, nil, "git", "clone", remote, repo)
	configureGitIdentity(t, repo)
	run(t, repo, nil, "git", "remote", "set-head", "origin", "-a")
	writeProgressConfig(t, repo)

	env := []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"PROGRESS_CONFIG_HOME=" + filepath.Join(tmp, "progress-config-home"),
	}

	implementation := run(t, repo, env, progressBin,
		"execution", "action",
		"--action", "start-implementation-pr",
		"--task-number", "101",
		"--title", "Проверить цикл исполнения и ревизии",
		"--task", "Выполнить первичную реализацию локальной проверочной задачи.",
	)
	requireContains(t, implementation, "state=completed")
	requireContains(t, implementation, "commit-message=Добавить дефектный результат e2e-прогона")

	review := run(t, repo, env, progressBin,
		"execution", "action",
		"--action", "review-pull-request",
		"--task-number", "101",
		"--title", "Проверить цикл исполнения и ревизии",
		"--task", "Провести ревизию результата локальной проверочной задачи.",
	)
	requireContains(t, review, "state=completed")
	requireContains(t, review, `"severity":"blocking"`)
	requireContains(t, review, `"id":"remark-1"`)

	rework := run(t, repo, env, progressBin,
		"execution", "action",
		"--action", "apply-review-comments",
		"--task-number", "101",
		"--title", "Проверить цикл исполнения и ревизии",
		"--task", "Исправить замечание ревизии локальной проверочной задачи.",
		"--review-remark", `{"id":"remark-1","severity":"blocking","title":"Некорректное значение","body":"Заменить дефектный результат на исправленный."}`,
	)
	requireContains(t, rework, "state=completed")
	requireContains(t, rework, "commit-message=Исправить замечание ревизии e2e")

	workplace := filepath.Join(repo, ".progress", "workplaces", "101")
	result := readFile(t, filepath.Join(workplace, "result.txt"))
	requireContains(t, result, "fixed=true")

	remoteTree := run(t, repo, nil, "git", "ls-tree", "-r", "--name-only", "origin/101")
	requireContains(t, remoteTree, "result.txt")
	requireNotContains(t, remoteTree, ".progress/runner-output")
	requireNotContains(t, remoteTree, ".progress/execution-runs")

	log := run(t, workplace, nil, "git", "log", "--format=%s", "-2")
	requireContains(t, log, "Исправить замечание ревизии e2e")
	requireContains(t, log, "Добавить дефектный результат e2e-прогона")
}

func findRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func configureGitIdentity(t *testing.T, dir string) {
	t.Helper()

	run(t, dir, nil, "git", "config", "user.name", "Progress E2E")
	run(t, dir, nil, "git", "config", "user.email", "progress-e2e@example.invalid")
}

func writeProgressConfig(t *testing.T, repo string) {
	t.Helper()

	writeFile(t, filepath.Join(repo, ".progress", "execution", "profiles.json"), profilesJSON(), 0o644)
	writeFile(t, filepath.Join(repo, ".progress", "execution", "resources.json"), resourcesJSON(), 0o644)
	writeFile(t, filepath.Join(repo, ".progress", "methodology", "catalog.json"), methodologyCatalogJSON(), 0o644)
}

func run(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed in %s: %v\n%s", name, strings.Join(args, " "), dir, err, string(output))
	}
	return string(output)
}

func writeFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func requireContains(t *testing.T, value string, fragment string) {
	t.Helper()

	if !strings.Contains(value, fragment) {
		t.Fatalf("expected output to contain %q\n%s", fragment, value)
	}
}

func requireNotContains(t *testing.T, value string, fragment string) {
	t.Helper()

	if strings.Contains(value, fragment) {
		t.Fatalf("expected output not to contain %q\n%s", fragment, value)
	}
}

func fakeOpenCodeScript() string {
	return `#!/bin/sh
set -eu

if [ "${1:-}" = "session" ]; then
	exit 0
fi

dir=""
prompt=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--dir)
			dir="$2"
			shift 2
			;;
		--model|--title)
			shift 2
			;;
		run)
			shift
			;;
		*)
			prompt="$1"
			shift
			;;
	esac
done

if [ -z "$dir" ]; then
	echo "execution directory is required" >&2
	exit 1
fi

mkdir -p "$dir/.progress/runner-output"
printf 'runtime trace\n' > "$dir/.progress/runner-output/e2e-local-diagnostic.txt"

case "$prompt" in
	*"Исправить замечание ревизии локальной проверочной задачи"*)
		printf 'base\nfixed=true\n' > "$dir/result.txt"
		echo "Замечание ревизии исправлено."
		cat <<'JSON'
<progress-structured-output>
{"summary":"Замечание ревизии исправлено.","commit_message":"Исправить замечание ревизии e2e","review_responses":[{"remark_id":"remark-1","status":"resolved","summary":"Значение исправлено."}],"changes":[{"summary":"Дефектный результат заменён исправленным."}],"commands":[{"name":"go test","args":["./e2e/execution-review-cycle"]}],"conclusion":{"status":"ok","summary":"Доработка завершена."}}
</progress-structured-output>
JSON
		;;
	*"Провести ревизию"*)
		echo "Ревизия выявила блокирующее замечание."
		cat <<'JSON'
<progress-structured-output>
{"summary":"Ревизия выявила блокирующее замечание.","remarks":[{"id":"remark-1","severity":"blocking","status":"open","title":"Некорректное значение","body":"Файл result.txt содержит defect=true вместо fixed=true."}],"conclusion":{"status":"needs-rework","summary":"Требуется доработка."}}
</progress-structured-output>
JSON
		;;
	*)
		printf 'base\ndefect=true\n' > "$dir/result.txt"
		echo "Первичная реализация выполнена."
		cat <<'JSON'
<progress-structured-output>
{"summary":"Первичная реализация выполнена.","commit_message":"Добавить дефектный результат e2e-прогона","changes":[{"summary":"Добавлен проверочный результат с намеренным дефектом."}],"commands":[{"name":"go test","args":["./e2e/execution-review-cycle"]}],"conclusion":{"status":"ok","summary":"Результат передан на ревизию."}}
</progress-structured-output>
JSON
		;;
esac
`
}

func profilesJSON() string {
	return `{
  "defaults": {
    "mode": "manual",
    "model-binding": "default",
    "allow-model-fallback": false,
    "structured-output": true,
    "structured-output-required": true,
    "structured-output-fields": ["summary", "commit_message", "remarks", "review_responses", "changes", "commands", "conclusion"]
  },
  "profiles": {
    "default": {
      "description": "Базовый профиль e2e-прогона"
    },
    "coder": {
      "description": "Профиль реализации e2e-прогона",
      "model-binding": "coder"
    },
    "review": {
      "description": "Профиль ревизии e2e-прогона",
      "model-binding": "review",
      "structured-output-fields": ["summary", "remarks", "conclusion"]
    }
  }
}
`
}

func resourcesJSON() string {
	return `{
  "defaults": {
    "model-binding": "default",
    "environment": "worktree"
  },
  "environments": {
    "worktree": {"type": "worktree", "enabled": true}
  },
  "tools": {
    "opencode": {"type": "agentic-system", "enabled": true}
  },
  "resources": {
    "e2e-model": {"type": "model", "enabled": true, "tools": ["opencode"]}
  },
  "bindings": {
    "default": {"tool": "opencode", "resource": "e2e-model", "environment": "worktree"},
    "coder": {"tool": "opencode", "resource": "e2e-model", "environment": "worktree"},
    "review": {"tool": "opencode", "resource": "e2e-model", "environment": "worktree"}
  }
}
`
}

func methodologyCatalogJSON() string {
	return `{
  "actions": [
    {
      "name": "start-implementation-pr",
      "class": "engineering-synthesis",
      "profile": "coder",
      "requires_workplace": true,
      "requires_synthesis": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "required": true},
        {"name": "resolve-profile", "kind": "resolve-profile", "required": true},
        {"name": "allocate-resources", "kind": "allocate-resources", "required": true},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "required": true},
        {"name": "build-directive", "kind": "build-directive", "required": true},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "required": true},
        {"name": "parse-result", "kind": "parse-result", "required": true},
        {"name": "commit-push", "kind": "commit-push", "required": true},
        {"name": "finalize", "kind": "finalize", "required": true}
      ]
    },
    {
      "name": "review-pull-request",
      "class": "review",
      "profile": "review",
      "requires_workplace": true,
      "requires_synthesis": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "required": true},
        {"name": "resolve-profile", "kind": "resolve-profile", "required": true},
        {"name": "allocate-resources", "kind": "allocate-resources", "required": true},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "required": true},
        {"name": "build-directive", "kind": "build-directive", "required": true},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "required": true},
        {"name": "parse-result", "kind": "parse-result", "required": true},
        {"name": "finalize", "kind": "finalize", "required": true}
      ]
    },
    {
      "name": "apply-review-comments",
      "class": "engineering-synthesis",
      "profile": "coder",
      "requires_workplace": true,
      "requires_synthesis": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "required": true},
        {"name": "resolve-profile", "kind": "resolve-profile", "required": true},
        {"name": "allocate-resources", "kind": "allocate-resources", "required": true},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "required": true},
        {"name": "build-directive", "kind": "build-directive", "required": true},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "required": true},
        {"name": "parse-result", "kind": "parse-result", "required": true},
        {"name": "commit-push", "kind": "commit-push", "required": true},
        {"name": "finalize", "kind": "finalize", "required": true}
      ]
    }
  ]
}
`
}

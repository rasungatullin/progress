#!/usr/bin/env python3
"""Проверка локальных ссылок и выбранных CLI-примеров документации."""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path


LINK_RE = re.compile(r"!?(?:\[[^]]*\])\(([^)]+)\)")
HEADING_RE = re.compile(r"^#{1,6}\s+(.+?)\s*#*\s*$")

CLI_CHECKS = (
    ("README.md", ("--help",), "Доступные команды:"),
    ("README.md", ("decision", "start", "--task", "123", "--help"), "Порядок вызова:"),
    ("docs/contours/decision/CLI.md", ("decision", "--help"), "Контур принятия решения"),
    ("docs/contours/execution/CLI.md", ("execution", "--help"), "Контур исполнения"),
    ("docs/contours/integration/CLI.md", ("integration", "--help"), "Контур интеграции"),
)


def anchor(value: str) -> str:
    value = re.sub(r"[`*_~]", "", value).strip().lower()
    value = re.sub(r"[^\w\s-]", "", value, flags=re.UNICODE)
    return re.sub(r"[-\s]+", "-", value).strip("-")


def headings(path: Path) -> set[str]:
    return {anchor(match.group(1)) for match in map(HEADING_RE.match, path.read_text().splitlines()) if match}


def markdown_files(root: Path) -> list[Path]:
    return [path for path in root.rglob("*.md") if ".git" not in path.parts and ".progress" not in path.parts]


def check_links(root: Path) -> list[str]:
    errors: list[str] = []
    for source in markdown_files(root):
        for line_number, line in enumerate(source.read_text().splitlines(), 1):
            for raw_target in LINK_RE.findall(line):
                target = raw_target.strip().strip("<>").split()[0]
                if not target or target.startswith(("http://", "https://", "mailto:")):
                    continue
                path_part, _, fragment = target.partition("#")
                if not path_part:
                    resolved = source
                else:
                    resolved = (source.parent / path_part).resolve()
                if not resolved.is_file() or (root not in resolved.parents and resolved != root):
                    errors.append(f"{source.relative_to(root)}:{line_number}: отсутствует файл {path_part or source.name}")
                elif fragment and anchor(fragment) not in headings(resolved):
                    errors.append(f"{source.relative_to(root)}:{line_number}: отсутствует якорь #{fragment}")
    return errors


def check_cli(root: Path) -> list[str]:
    errors: list[str] = []
    with tempfile.TemporaryDirectory(prefix="progress-docs-") as cache:
        for document, args, expected in CLI_CHECKS:
            result = subprocess.run(
                ["go", "run", "./cmd/progress", *args],
                cwd=root,
                env={**os.environ, "GOCACHE": cache},
                capture_output=True,
                text=True,
            )
            output = result.stdout + result.stderr
            if result.returncode != 0:
                errors.append(f"{document}: команда progress {' '.join(args)} завершилась с кодом {result.returncode}")
            elif expected not in output:
                errors.append(f"{document}: вывод progress {' '.join(args)} не содержит {expected!r}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    args = parser.parse_args()
    errors = check_links(args.root) + check_cli(args.root)
    if errors:
        print("Проверка документации завершилась с ошибками:", file=sys.stderr)
        print("\n".join(f"- {error}" for error in errors), file=sys.stderr)
        return 1
    print("Проверка документации: локальные ссылки и CLI-примеры корректны.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

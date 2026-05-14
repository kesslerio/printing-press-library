---
name: macctx
description: Give agents eyes, context, and guarded hands on macOS through a Peekaboo-backed CLI.
metadata:
  openclaw:
    os: [darwin]
    requires:
      bins: [macctx-pp-cli, peekaboo]
---

# macctx

Use `macctx-pp-cli` when an agent needs local Mac context or approval-gated computer-use actions.

## Core pattern

```bash
macctx-pp-cli dump --json --screenshot --see
macctx-pp-cli handoff --clipboard
macctx-pp-cli act click --on B3          # dry-run proposal
macctx-pp-cli act click --on B3 --execute # real action after approval
```

## Safety

- Observation commands are local-first and call Peekaboo with `--no-remote`.
- Clipboard output is preview-only unless `--full` is explicitly passed.
- `act` commands are dry-run by default and require `--execute` for real UI actions.
- Use human approval before destructive UI actions.

## Useful commands

```bash
macctx-pp-cli doctor --json
macctx-pp-cli active --json
macctx-pp-cli see --annotate --path /tmp/ui.png --json
macctx-pp-cli screenshot --window --path /tmp/window.png --json
macctx-pp-cli dump --json --screenshot --see --clipboard
macctx-pp-cli handoff --clipboard
macctx-pp-cli act hotkey --keys cmd,s
macctx-pp-cli act type "hello" --return
```

// Copyright 2026 mvanhorn. Licensed under Apache-2.0. See LICENSE.

// Package playbooks ships the prediction-goat-specific playbook +
// notes content as an embedded filesystem. The auto-install path in
// internal/cli/playbook_init.go reads from FS at first DB open and
// seeds the learning_playbooks table.
//
// Convention (designed to copy cleanly to every CLI):
//   - <family>.json holds the steps + entity_slots + expected_tool_calls
//   - <family>_notes.md holds gotchas / workarounds (read verbatim
//     by the agent at recall time)
//   - MANIFEST.md keeps //go:embed *.md matching at least one file
//     even when no playbook content exists
//
// Bump SeedVersion when the embedded content changes so existing
// installs re-seed on the next CLI invocation.
//
// PATCH(learn-loop-backport U9): ported from ESPN PR #851 HEAD
// 9bb0a40a (library/media-and-entertainment/espn/internal/cli/
// playbooks/embed.go). SeedVersion flavored for prediction-goat;
// hand-authored content arrives in U10.

package playbooks

import "embed"

// The *.md pattern matches MANIFEST.md so the embed declaration is
// well-formed even when no playbook content has shipped yet. The
// *.json pattern picks up hand-authored playbook JSONs when they
// arrive (U10). Until then, FS contains only MANIFEST.md.
//
//go:embed *.md
var FS embed.FS

// SeedVersion identifies the playbook content version. Embedded by
// the install path as a sentinel row; mismatch triggers re-seed.
// Format: <iso-date>-<cli-name>-<content-rev>.
var SeedVersion = "2026-05-26-prediction-goat-001"

// Copyright 2026 mvanhorn. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestFTSContent_StripsRawJSONNoise covers the core regression that
// motivated U2: the live store indexes the entire raw JSON blob in
// resources_fts.content, so KXFUSION (a Kalshi "Nuclear fusion" series)
// matches a query like "oscars best picture" because its source_agencies
// field cites the Academy of Motion Picture Arts and Sciences at
// oscars.org. After U2, ftsContent projects to title/ticker/category
// only, so the false-positive vector is gone.
//
// Characterization-first: the table runs against ftsContent directly
// (no DB) so the assertions about WHAT we index versus what we drop are
// independent of the FTS engine's tokenizer.
func TestFTSContent_StripsRawJSONNoise(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		data           string
		mustContain    []string
		mustNotContain []string
	}{
		{
			name: "kalshi_series_nuclear_fusion drops source_agencies",
			data: `{
				"ticker": "KXFUSION",
				"title": "Nuclear fusion",
				"category": "Science",
				"source_agencies": [
					{"name": "Academy of Motion Picture Arts and Sciences", "url": "https://www.oscars.org/oscars"},
					{"name": "Financial Times", "url": "https://ft.com/"}
				],
				"additional_prohibitions": ["Persons employed by any of the Source Agencies"]
			}`,
			mustContain:    []string{"Nuclear fusion", "KXFUSION", "Science"},
			mustNotContain: []string{"oscars", "Motion Picture", "Academy", "Financial Times", "prohibitions", "employed"},
		},
		{
			name: "kalshi_market_portugal_world_cup keeps real signal",
			data: `{
				"ticker": "KXMENWORLDCUP-26-PT",
				"event_ticker": "KXMENWORLDCUP-26",
				"title": "Will the Portugal win the 2026 Men's World Cup?",
				"yes_sub_title": "Portugal",
				"category": "Sports"
			}`,
			mustContain: []string{"Portugal", "World Cup", "KXMENWORLDCUP-26-PT", "KXMENWORLDCUP-26", "Sports"},
		},
		{
			name: "polymarket_market keeps question + slug + category",
			data: `{
				"slug": "will-portugal-win-the-2026-fifa-world-cup-912",
				"question": "Will Portugal win the 2026 FIFA World Cup?",
				"category": "Sports",
				"description": "Will Portugal lift the 2026 FIFA World Cup trophy on July 20?",
				"endDate": "2026-07-20T00:00:00Z"
			}`,
			mustContain:    []string{"Will Portugal win", "FIFA World Cup", "will-portugal-win-the-2026-fifa-world-cup-912", "Sports"},
			mustNotContain: []string{"endDate", "T00:00:00Z"},
		},
		{
			name: "kalshi_series_oscar keeps oscar signal",
			data: `{
				"ticker": "KXOSCARCOUNTCONCLAVE",
				"title": "Conclave Oscar wins",
				"category": "Entertainment"
			}`,
			mustContain: []string{"Conclave Oscar wins", "KXOSCARCOUNTCONCLAVE", "Entertainment"},
		},
		{
			name: "kalshi_series_bitcoin keeps bitcoin signal",
			data: `{
				"ticker": "KXBTCMAX100",
				"title": "When will bitcoin hit 100k?",
				"category": "Crypto"
			}`,
			mustContain: []string{"bitcoin hit 100k", "KXBTCMAX100", "Crypto"},
		},
		{
			name: "tag projects to label + slug",
			data: `{
				"id": "77",
				"label": "mavericks",
				"slug": "mavericks",
				"publishedAt": "2023-11-02 21:18:00"
			}`,
			mustContain:    []string{"mavericks"},
			mustNotContain: []string{"publishedAt", "2023-11-02"},
		},
		{
			name:        "malformed json returns empty",
			data:        `not-json{`,
			mustContain: []string{},
		},
		{
			name:        "unknown resource type with name field",
			data:        `{"name": "legacy restaurant", "kind": "biz"}`,
			mustContain: []string{"legacy restaurant"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ftsContent(tc.data)
			for _, want := range tc.mustContain {
				if !strings.Contains(got, want) {
					t.Errorf("ftsContent output missing required substring %q\ngot: %q", want, got)
				}
			}
			for _, forbidden := range tc.mustNotContain {
				if strings.Contains(strings.ToLower(got), strings.ToLower(forbidden)) {
					t.Errorf("ftsContent output contains forbidden substring %q (case-insensitive)\ngot: %q", forbidden, got)
				}
			}
		})
	}
}

// TestFTSContent_PreventsFTSFalsePositives runs end-to-end against a
// real SQLite + FTS5 instance. Seeds KXFUSION and KXOSCARCOUNTCONCLAVE,
// then runs MATCH queries that pre-U2 would have surfaced KXFUSION as
// a top hit for "oscars". After U2 the FTS index only contains the
// curated title+ticker+category, so KXFUSION cannot match.
func TestFTSContent_PreventsFTSFalsePositives(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "fts_content.db")
	s, err := OpenWithContext(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenWithContext: %v", err)
	}
	defer s.Close()

	fixtures := []struct {
		resourceType string
		id           string
		data         string
	}{
		{
			resourceType: "kalshi_series",
			id:           "KXFUSION",
			data: `{
				"ticker": "KXFUSION",
				"title": "Nuclear fusion",
				"category": "Science",
				"source_agencies": [{"name": "Academy of Motion Picture Arts and Sciences", "url": "https://www.oscars.org/oscars"}]
			}`,
		},
		{
			resourceType: "kalshi_series",
			id:           "KXOSCARCOUNTCONCLAVE",
			data:         `{"ticker": "KXOSCARCOUNTCONCLAVE", "title": "Conclave Oscar wins", "category": "Entertainment"}`,
		},
		{
			resourceType: "kalshi_series",
			id:           "KXBTCMAX100",
			data:         `{"ticker": "KXBTCMAX100", "title": "When will bitcoin hit 100k?", "category": "Crypto"}`,
		},
		{
			resourceType: "kalshi_markets",
			id:           "KXMENWORLDCUP-26-PT",
			data: `{
				"ticker": "KXMENWORLDCUP-26-PT",
				"event_ticker": "KXMENWORLDCUP-26",
				"title": "Will the Portugal win the 2026 Men's World Cup?",
				"yes_sub_title": "Portugal",
				"category": "Sports"
			}`,
		},
	}
	for _, f := range fixtures {
		if err := s.Upsert(f.resourceType, f.id, json.RawMessage(f.data)); err != nil {
			t.Fatalf("upsert %s: %v", f.id, err)
		}
	}

	queries := []struct {
		name        string
		match       string
		mustReturn  []string // resource_ids that must appear
		mustExclude []string // resource_ids that must NOT appear
	}{
		{
			name:        "oscar query excludes Nuclear fusion",
			match:       "oscar",
			mustReturn:  []string{"KXOSCARCOUNTCONCLAVE"},
			mustExclude: []string{"KXFUSION"},
		},
		{
			name:        "bitcoin query finds BTC series",
			match:       "bitcoin",
			mustReturn:  []string{"KXBTCMAX100"},
			mustExclude: []string{"KXFUSION", "KXOSCARCOUNTCONCLAVE"},
		},
		{
			name:        "portugal world cup query finds the right Kalshi market",
			match:       "portugal world cup",
			mustReturn:  []string{"KXMENWORLDCUP-26-PT"},
			mustExclude: []string{"KXFUSION"},
		},
		{
			name:        "ticker prefix matches exact",
			match:       "KXMENWORLDCUP",
			mustReturn:  []string{"KXMENWORLDCUP-26-PT"},
			mustExclude: []string{"KXFUSION"},
		},
	}

	for _, q := range queries {
		t.Run(q.name, func(t *testing.T) {
			rows, err := s.DB().Query(
				`SELECT id FROM resources_fts WHERE resources_fts MATCH ?`,
				q.match,
			)
			if err != nil {
				t.Fatalf("query %q: %v", q.match, err)
			}
			defer rows.Close()
			seen := map[string]bool{}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Fatalf("scan: %v", err)
				}
				seen[id] = true
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows: %v", err)
			}
			for _, must := range q.mustReturn {
				if !seen[must] {
					t.Errorf("query %q: missing required hit %q (seen=%v)", q.match, must, seen)
				}
			}
			for _, mustNot := range q.mustExclude {
				if seen[mustNot] {
					t.Errorf("query %q: surfaced forbidden hit %q (seen=%v)", q.match, mustNot, seen)
				}
			}
		})
	}
}

// TestSchemaVersionAt3 pins the version bump so future migrations have
// a clean baseline.
func TestSchemaVersionAt3(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "version.db")
	s, err := OpenWithContext(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenWithContext: %v", err)
	}
	defer s.Close()
	v, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != 3 {
		t.Errorf("SchemaVersion = %d, want 3", v)
	}
}

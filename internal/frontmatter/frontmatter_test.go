package frontmatter

import (
	"bytes"
	"testing"
)

func TestParseSource(t *testing.T) {
	input := []byte(`---
title: "Attention Mechanisms"
source_url: "https://example.com"
author: "Author Name"
date_added: "2025-04-20"
type: "article"
tags: ["tag1", "tag2"]
status: "uncompiled"
---
This is the body content of the source markdown file.
`)

	sf, body, err := ParseSource(input)
	if err != nil {
		t.Fatalf("ParseSource failed: %v", err)
	}

	if sf.Title != "Attention Mechanisms" {
		t.Errorf("expected Title 'Attention Mechanisms', got '%s'", sf.Title)
	}
	if sf.SourceURL != "https://example.com" {
		t.Errorf("expected SourceURL 'https://example.com', got '%s'", sf.SourceURL)
	}
	if len(sf.Tags) != 2 || sf.Tags[0] != "tag1" || sf.Tags[1] != "tag2" {
		t.Errorf("unexpected tags: %v", sf.Tags)
	}
	if sf.Status != "uncompiled" {
		t.Errorf("expected Status 'uncompiled', got '%s'", sf.Status)
	}

	expectedBody := "This is the body content of the source markdown file."
	if bytes.TrimSpace([]byte(body)) == nil || bytes.TrimSpace([]byte(body))[0] != expectedBody[0] {
		t.Errorf("expected body to contain '%s', got '%s'", expectedBody, body)
	}
}

func TestParseWiki(t *testing.T) {
	input := []byte(`---
title: "Article Title"
type: "concept"
tags: ["tag1", "tag2"]
created: "2025-04-20"
updated: "2025-04-20"
sources: ["[[source-wikilink]]"]
related: ["[[related-article]]"]
provenance: "extracted"
summary: "One sentence summary."
compiled_from: "source.md"
---
Wiki body content here.
`)

	wf, body, err := ParseWiki(input)
	if err != nil {
		t.Fatalf("ParseWiki failed: %v", err)
	}

	if wf.Title != "Article Title" {
		t.Errorf("expected Title 'Article Title', got '%s'", wf.Title)
	}
	if wf.Provenance != "extracted" {
		t.Errorf("expected Provenance 'extracted', got '%s'", wf.Provenance)
	}
	if wf.CompiledFrom != "source.md" {
		t.Errorf("expected CompiledFrom 'source.md', got '%s'", wf.CompiledFrom)
	}

	expectedBody := "Wiki body content here."
	if bytes.TrimSpace([]byte(body)) == nil || bytes.TrimSpace([]byte(body))[0] != expectedBody[0] {
		t.Errorf("expected body to contain '%s', got '%s'", expectedBody, body)
	}
}

func TestMarshalAndParseRoundtrip(t *testing.T) {
	sf := &SourceFrontmatter{
		Title:     "Roundtrip Test",
		SourceURL: "https://rt.com",
		Author:    "RT",
		DateAdded: "2026-05-21",
		Type:      "tool",
		Tags:      []string{"rt1", "rt2"},
		Status:    "compiled",
	}
	body := "Body content for roundtrip check."

	marshaled, err := Marshal(sf, body)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	sfParsed, bodyParsed, err := ParseSource(marshaled)
	if err != nil {
		t.Fatalf("ParseSource roundtrip failed: %v", err)
	}

	if sfParsed.Title != sf.Title || sfParsed.SourceURL != sf.SourceURL || sfParsed.Status != sf.Status {
		t.Errorf("parsed values mismatch: %+v vs %+v", sfParsed, sf)
	}

	if bytes.TrimSpace([]byte(bodyParsed))[0] != body[0] {
		t.Errorf("body mismatch: expected '%s', got '%s'", body, bodyParsed)
	}
}

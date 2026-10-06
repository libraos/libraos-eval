// Package eval evaluates LibraOS digital employees: it runs a prompt or an
// installed agent against versioned cases, scores each reply with
// deterministic checks, and records an immutable, re-verifiable run.
package eval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Case kinds.
const (
	// KindDraft expects a JSON draft of cited claims and inert proposals. Quotes
	// are anchored to the case evidence and the draft is validated strictly.
	KindDraft = "draft"
	// KindChat expects free text. It is scored by the case's text checks.
	KindChat = "chat"
)

// Evidence is one immutable unit of source text a case supplies.
type Evidence struct {
	SourceID string `json:"source_id"`
	UnitID   string `json:"unit_id"`
	Locator  string `json:"locator"`
	Text     string `json:"text"`
}

// Expect describes a passing reply. Accept applies to draft cases: whether the
// anchored draft should pass validation. The text checks apply to chat cases
// and, when set, to the raw reply of draft cases.
type Expect struct {
	Accept       *bool    `json:"accept,omitempty"`
	MustMatch    []string `json:"must_match,omitempty"`
	MustNotMatch []string `json:"must_not_match,omitempty"`
	MaxWords     int      `json:"max_words,omitempty"`
}

// Case is one evaluation input. IDs must be unique within a case file.
type Case struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Title    string     `json:"title"`
	Input    string     `json:"input"`
	Evidence []Evidence `json:"evidence,omitempty"`
	Expect   Expect     `json:"expect"`
}

// LoadCases reads a JSONL case file (one case per line; blank lines and lines
// starting with # are ignored) and returns the cases and the file's digest.
func LoadCases(path string) ([]Case, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	cases, err := ParseCases(raw)
	return cases, Digest(raw), err
}

// ParseCases parses and validates JSONL case content.
func ParseCases(raw []byte) ([]Case, error) {
	var cases []Case
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1<<20), 8<<20)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text))
		dec.DisallowUnknownFields()
		var c Case
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("case line %d: %w", line, err)
		}
		if err := c.validate(); err != nil {
			return nil, fmt.Errorf("case line %d (%s): %w", line, c.ID, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("case line %d: duplicate id %q", line, c.ID)
		}
		seen[c.ID] = true
		cases = append(cases, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no cases")
	}
	return cases, nil
}

func (c Case) validate() error {
	if c.ID == "" || strings.TrimSpace(c.Input) == "" {
		return fmt.Errorf("id and input are required")
	}
	switch c.Kind {
	case KindDraft:
		if len(c.Evidence) == 0 {
			return fmt.Errorf("draft cases need evidence")
		}
		if c.Expect.Accept == nil {
			return fmt.Errorf("draft cases need expect.accept")
		}
	case KindChat:
		if c.Expect.Accept != nil {
			return fmt.Errorf("expect.accept applies to draft cases only")
		}
	default:
		return fmt.Errorf("kind must be %q or %q", KindDraft, KindChat)
	}
	units := map[string]bool{}
	for _, e := range c.Evidence {
		if e.SourceID == "" || e.UnitID == "" || e.Text == "" {
			return fmt.Errorf("evidence needs source_id, unit_id and text")
		}
		key := e.SourceID + "\x00" + e.UnitID
		if units[key] {
			return fmt.Errorf("duplicate evidence unit %s/%s", e.SourceID, e.UnitID)
		}
		units[key] = true
	}
	for _, p := range append(append([]string{}, c.Expect.MustMatch...), c.Expect.MustNotMatch...) {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("invalid pattern %q: %w", p, err)
		}
	}
	return nil
}

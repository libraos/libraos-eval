package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Draft is the structured output a draft case asks for: cited claims and
// inert proposals for a human. Proposals never execute anything.
type Draft struct {
	Claims    []Claim    `json:"claims"`
	Proposals []Proposal `json:"proposals"`
}
type Claim struct {
	Text      string     `json:"text"`
	Citations []Citation `json:"citations"`
}
type Proposal struct {
	Type                 string `json:"type"`
	Text                 string `json:"text"`
	ConfirmationRequired bool   `json:"confirmation_required"`
}

// InertProposalTypes are the only proposal types a draft may contain.
var InertProposalTypes = []string{"request_evidence", "draft_task", "draft_report"}

// Normalize maps a model reply onto the draft schema. Known fields are kept;
// each quote is anchored to the evidence; an unanchorable quote keeps zero
// offsets so validation rejects it. A reply without a JSON object is returned
// unchanged and fails as invalid JSON.
func Normalize(reply string, evidence []Evidence) (draft string, returned, anchored int) {
	text := stripReasoning(reply)
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	var in struct {
		Claims []struct {
			Text      string `json:"text"`
			Citations []struct {
				SourceID   string `json:"source_id"`
				UnitID     string `json:"unit_id"`
				QuotedText string `json:"quoted_text"`
			} `json:"citations"`
		} `json:"claims"`
		Proposals []map[string]any `json:"proposals"`
	}
	if start < 0 || end <= start || json.Unmarshal([]byte(text[start:end+1]), &in) != nil {
		return strings.TrimSpace(text), 0, 0
	}
	out := struct {
		Claims    []Claim          `json:"claims"`
		Proposals []map[string]any `json:"proposals"`
	}{Claims: []Claim{}, Proposals: []map[string]any{}}
	for _, c := range in.Claims {
		next := Claim{Text: c.Text, Citations: []Citation{}}
		for _, q := range c.Citations {
			returned++
			if a, ok := Anchor(evidence, q.SourceID, q.UnitID, q.QuotedText); ok {
				anchored++
				next.Citations = append(next.Citations, a)
			} else {
				next.Citations = append(next.Citations, Citation{SourceID: q.SourceID, UnitID: q.UnitID, QuotedText: q.QuotedText})
			}
		}
		out.Claims = append(out.Claims, next)
	}
	for _, p := range in.Proposals {
		kept := map[string]any{}
		for _, key := range []string{"type", "text", "confirmation_required"} {
			if v, ok := p[key]; ok {
				kept[key] = v
			}
		}
		out.Proposals = append(out.Proposals, kept)
	}
	raw, _ := json.Marshal(out)
	return string(raw), returned, anchored
}

var reasoning = regexp.MustCompile(`(?s)<think>.*?</think>`)

func stripReasoning(s string) string { return reasoning.ReplaceAllString(s, "") }

// ValidateDraft strictly checks a draft against the case evidence. It returns
// an empty code when the draft is accepted.
func ValidateDraft(draft string, evidence []Evidence) (code, message string) {
	raw := []byte(draft)
	if len(raw) > 128000 || !json.Valid(raw) {
		return "invalid_json", "draft must be valid JSON of at most 128000 bytes"
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return "invalid_json", err.Error()
	}
	if err := checkShape(raw); err != nil {
		return "invalid_shape", err.Error()
	}
	var d Draft
	if err := json.Unmarshal(raw, &d); err != nil {
		return "invalid_shape", "fields have invalid types"
	}
	for _, c := range d.Claims {
		if !boundedText(c.Text, 2000) {
			return "invalid_claim", "claim text is empty, invalid or too long"
		}
		for _, cite := range c.Citations {
			if msg := CheckCitation(cite, evidence); msg != "" {
				return "invalid_citation", msg
			}
		}
	}
	for _, p := range d.Proposals {
		if !contains(InertProposalTypes, p.Type) {
			return "prohibited_proposal", "only inert request_evidence, draft_task and draft_report proposals are permitted"
		}
		if !boundedText(p.Text, 2000) {
			return "invalid_proposal", "proposal text is empty, invalid or too long"
		}
		if !p.ConfirmationRequired {
			return "confirmation_required", "every proposal needs confirmation_required: true"
		}
	}
	return "", ""
}

func checkShape(raw []byte) error {
	obj, err := object(raw, "claims", "proposals")
	if err != nil {
		return err
	}
	claims, err := array(obj["claims"], 1, 20)
	if err != nil {
		return err
	}
	proposals, err := array(obj["proposals"], 0, 10)
	if err != nil {
		return err
	}
	for _, c := range claims {
		claim, err := object(c, "text", "citations")
		if err != nil {
			return err
		}
		cites, err := array(claim["citations"], 1, 8)
		if err != nil {
			return err
		}
		for _, cite := range cites {
			if _, err := object(cite, "source_id", "unit_id", "locator", "start_offset", "end_offset", "quoted_text"); err != nil {
				return err
			}
		}
	}
	for _, p := range proposals {
		if _, err := object(p, "type", "text", "confirmation_required"); err != nil {
			return err
		}
	}
	return nil
}

func object(raw json.RawMessage, keys ...string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '{' || json.Unmarshal(raw, &obj) != nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
		if _, ok := obj[k]; !ok {
			return nil, fmt.Errorf("missing required field %s", k)
		}
	}
	names := make([]string, 0, len(obj))
	for k := range obj {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if string(bytes.TrimSpace(obj[k])) == "null" {
			return nil, fmt.Errorf("null field %s is not permitted", k)
		}
		if !allowed[k] {
			return nil, fmt.Errorf("unknown field %s", k)
		}
	}
	return obj, nil
}

func array(raw json.RawMessage, min, max int) ([]json.RawMessage, error) {
	var xs []json.RawMessage
	if json.Unmarshal(raw, &xs) != nil || xs == nil || len(xs) < min || len(xs) > max {
		return nil, fmt.Errorf("expected an array with %d-%d items", min, max)
	}
	return xs, nil
}

// rejectDuplicateKeys walks the token stream and refuses repeated object keys,
// which encoding/json would otherwise silently resolve.
func rejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]bool{}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return err
				}
				key := k.(string)
				if seen[key] {
					return fmt.Errorf("duplicate field %s", key)
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case json.Delim('['):
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		return nil
	}
	if err := walk(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func boundedText(s string, max int) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= max
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

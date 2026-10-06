package eval

import (
	"regexp"
	"strings"
	"unicode"
)

// Citation is an exact, half-open Unicode code-point span of one evidence unit.
type Citation struct {
	SourceID    string `json:"source_id"`
	UnitID      string `json:"unit_id"`
	Locator     string `json:"locator"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
	QuotedText  string `json:"quoted_text"`
}

// MinQuoteRunes is the shortest quote that is anchored; shorter quotes match
// too easily to show that the model read the evidence.
const MinQuoteRunes = 5

// GatewayDateStamp matches the annotation LibraOS's inbound DateNormalizer
// guardrail appends to relative date phrases in every user message, including
// quoted evidence: "today" becomes "today (2026-10-06)". A model that quotes
// the rewritten evidence faithfully would otherwise fail exact anchoring.
var GatewayDateStamp = regexp.MustCompile(`(?i)\b(the day before yesterday|day after tomorrow|tomorrow|yesterday|today) \(\d{4}-\d{2}-\d{2}\)`)

// Anchor finds a model quote in the named unit, then in any unit of the same
// source. Matching tolerates whitespace runs and typographic quote or dash
// differences only. If that fails, gateway date stamps are removed and the
// match is retried. The returned QuotedText is always the exact source span.
func Anchor(evidence []Evidence, sourceID, unitID, quote string) (Citation, bool) {
	if c, ok := anchorExact(evidence, sourceID, unitID, quote); ok {
		return c, true
	}
	if stripped := GatewayDateStamp.ReplaceAllString(quote, "$1"); stripped != quote {
		return anchorExact(evidence, sourceID, unitID, stripped)
	}
	return Citation{}, false
}

func anchorExact(evidence []Evidence, sourceID, unitID, quote string) (Citation, bool) {
	quote = strings.TrimSpace(quote)
	if len([]rune(quote)) < MinQuoteRunes {
		return Citation{}, false
	}
	for pass := 0; pass < 2; pass++ {
		for _, e := range evidence {
			if e.SourceID != sourceID || (pass == 0) != (e.UnitID == unitID) {
				continue
			}
			if from, to, ok := findLoose(e.Text, quote); ok {
				return Citation{SourceID: e.SourceID, UnitID: e.UnitID, Locator: e.Locator, StartOffset: from, EndOffset: to, QuotedText: string([]rune(e.Text)[from:to])}, true
			}
		}
	}
	return Citation{}, false
}

// CheckCitation verifies that a citation is an exact span of the evidence.
func CheckCitation(c Citation, evidence []Evidence) string {
	for _, e := range evidence {
		if e.SourceID != c.SourceID || e.UnitID != c.UnitID {
			continue
		}
		r := []rune(e.Text)
		if c.Locator != e.Locator || c.StartOffset < 0 || c.EndOffset <= c.StartOffset || c.EndOffset > len(r) {
			return "citation locator or Unicode offsets are invalid"
		}
		if string(r[c.StartOffset:c.EndOffset]) != c.QuotedText {
			return "quoted text does not match the evidence unit"
		}
		return ""
	}
	return "cited evidence unit is not part of this case"
}

func findLoose(haystack, needle string) (int, int, bool) {
	hay, index := normalize([]rune(haystack))
	ndl, _ := normalize([]rune(needle))
	if len(ndl) == 0 {
		return 0, 0, false
	}
	at := strings.Index(string(hay), string(ndl))
	if at < 0 {
		return 0, 0, false
	}
	first := len([]rune(string(hay)[:at]))
	return index[first], index[first+len(ndl)-1] + 1, true
}

func normalize(in []rune) ([]rune, []int) {
	out, index := make([]rune, 0, len(in)), make([]int, 0, len(in))
	space := false
	for i, r := range in {
		switch r {
		case '‘', '’', '‛', '′':
			r = '\''
		case '“', '”', '‟', '″':
			r = '"'
		case '–', '—', '−':
			r = '-'
		}
		if unicode.IsSpace(r) {
			if space || len(out) == 0 {
				continue
			}
			space, r = true, ' '
		} else {
			space = false
		}
		out = append(out, r)
		index = append(index, i)
	}
	if len(out) > 0 && out[len(out)-1] == ' ' {
		out, index = out[:len(out)-1], index[:len(index)-1]
	}
	return out, index
}

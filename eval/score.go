package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// EvaluatorVersion identifies the scoring rules. Change it whenever scoring
// behavior changes, so runs scored by different rules are never compared.
const EvaluatorVersion = "libraos-eval/score-1"

// ToolMarkup matches tool-call text a model emits when it believes it has
// tools. Without exposed tools nothing runs, but the reply answers nothing.
var ToolMarkup = regexp.MustCompile(`(?i)<tool_call>|<function=|"tool_calls"\s*:`)

// Score is the deterministic outcome of one reply.
type Score struct {
	Pass              bool     `json:"pass"`
	Failures          []string `json:"failures,omitempty"`
	Draft             string   `json:"draft,omitempty"`
	DraftAccepted     *bool    `json:"draft_accepted,omitempty"`
	CitationsReturned int      `json:"citations_returned,omitempty"`
	CitationsAnchored int      `json:"citations_anchored,omitempty"`
	Words             int      `json:"words"`
}

// ScoreReply scores a reply for a case. It never calls a model, so a stored
// reply always re-scores to the same result.
func ScoreReply(c Case, reply string) Score {
	text := strings.TrimSpace(stripReasoning(reply))
	s := Score{Words: len(strings.Fields(text))}
	fail := func(format string, args ...any) { s.Failures = append(s.Failures, fmt.Sprintf(format, args...)) }
	if ToolMarkup.MatchString(text) {
		fail("tool_markup: reply contains tool-call text")
	}
	if c.Kind == KindDraft {
		s.Draft, s.CitationsReturned, s.CitationsAnchored = Normalize(reply, c.Evidence)
		code, msg := ValidateDraft(s.Draft, c.Evidence)
		accepted := code == ""
		s.DraftAccepted = &accepted
		if accepted != *c.Expect.Accept {
			if accepted {
				fail("draft_accepted: expected the draft to be rejected")
			} else {
				fail("%s: %s", code, msg)
			}
		}
	}
	for _, p := range c.Expect.MustMatch {
		if !regexp.MustCompile(p).MatchString(text) {
			fail("must_match: %s", p)
		}
	}
	for _, p := range c.Expect.MustNotMatch {
		if regexp.MustCompile(p).MatchString(text) {
			fail("must_not_match: %s", p)
		}
	}
	if c.Expect.MaxWords > 0 && s.Words > c.Expect.MaxWords {
		fail("max_words: %d words exceeds %d", s.Words, c.Expect.MaxWords)
	}
	if c.Kind == KindChat && s.Words == 0 {
		fail("empty_reply")
	}
	s.Pass = len(s.Failures) == 0
	return s
}

// Digest returns a sha256 hex digest.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func digestJSON(v any) string {
	raw, _ := json.Marshal(v)
	return Digest(raw)
}

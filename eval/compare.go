package eval

import "fmt"

// CaseDelta is one case's outcome in two runs.
type CaseDelta struct {
	CaseID    string `json:"case_id"`
	Title     string `json:"title"`
	Before    bool   `json:"before"`
	After     bool   `json:"after"`
	Change    string `json:"change"`
	BeforeWhy string `json:"before_why,omitempty"`
	AfterWhy  string `json:"after_why,omitempty"`
}

// Comparison contrasts two runs over the same case file and scoring rules.
type Comparison struct {
	Before, After       string      `json:"-"`
	BeforeAgent         string      `json:"before_agent"`
	AfterAgent          string      `json:"after_agent"`
	BeforePassed        int         `json:"before_passed"`
	AfterPassed         int         `json:"after_passed"`
	Total               int         `json:"total"`
	BeforeAnchoredRatio float64     `json:"before_anchored_ratio"`
	AfterAnchoredRatio  float64     `json:"after_anchored_ratio"`
	Cases               []CaseDelta `json:"cases"`
	Note                string      `json:"note"`
}

// Compare refuses runs that are not comparable: different cases or scoring.
func Compare(before, after *Run) (*Comparison, error) {
	if before.CasesSHA256 != after.CasesSHA256 || before.EvaluatorVersion != after.EvaluatorVersion {
		return nil, fmt.Errorf("runs are not comparable: they need the same case file and evaluator version")
	}
	c := &Comparison{BeforeAgent: before.Agent, AfterAgent: after.Agent, BeforePassed: before.Summary.Passed, AfterPassed: after.Summary.Passed, Total: before.Summary.Total, BeforeAnchoredRatio: ratio(before.Summary), AfterAnchoredRatio: ratio(after.Summary),
		Note: "One run per side: a single sample of model behavior. Re-run both sides before treating a small difference as real."}
	for i, b := range before.Results {
		a := after.Results[i]
		change := "same"
		if b.Pass != a.Pass {
			change = map[bool]string{true: "fixed", false: "regressed"}[a.Pass]
		}
		c.Cases = append(c.Cases, CaseDelta{CaseID: b.CaseID, Title: b.Title, Before: b.Pass, After: a.Pass, Change: change, BeforeWhy: first(b.Failures), AfterWhy: first(a.Failures)})
	}
	return c, nil
}

func ratio(s Summary) float64 {
	if s.CitationsReturned == 0 {
		return 0
	}
	return float64(s.CitationsAnchored) / float64(s.CitationsReturned)
}

func first(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[0]
}

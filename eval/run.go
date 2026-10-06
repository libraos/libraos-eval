package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// ToolVersion is reported in every run record.
const ToolVersion = "0.1.0"

// Result is one executed case. Reply is stored verbatim so the score can be
// recomputed by Verify.
type Result struct {
	CaseID  string `json:"case_id"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Job
	Score
}

// Summary aggregates a run.
type Summary struct {
	Passed            int `json:"passed"`
	Total             int `json:"total"`
	CitationsReturned int `json:"citations_returned"`
	CitationsAnchored int `json:"citations_anchored"`
	MeanLatencyMS     int `json:"mean_latency_ms"`
}

// Run is an immutable evaluation record. Digest covers every other field.
type Run struct {
	ToolVersion      string   `json:"tool_version"`
	EvaluatorVersion string   `json:"evaluator_version"`
	Label            string   `json:"label,omitempty"`
	StartedAt        string   `json:"started_at"`
	FinishedAt       string   `json:"finished_at"`
	Gateway          string   `json:"gateway"`
	Agent            string   `json:"agent"`
	Model            string   `json:"model,omitempty"`
	PromptSHA256     string   `json:"prompt_sha256,omitempty"`
	CasesSHA256      string   `json:"cases_sha256"`
	Cases            []Case   `json:"cases"`
	Results          []Result `json:"results"`
	Summary          Summary  `json:"summary"`
	Digest           string   `json:"digest"`
}

// Target names what to evaluate: a prompt to install, or an existing agent.
type Target struct {
	Prompt          string
	Agent           string
	Model           string
	MaxOutputTokens int
}

// Execute runs every case once, in order, and returns a sealed run. Progress,
// when set, is called after each case.
func Execute(ctx context.Context, g *Gateway, target Target, cases []Case, casesDigest, label string, progress func(done, total int)) (*Run, error) {
	r := &Run{ToolVersion: ToolVersion, EvaluatorVersion: EvaluatorVersion, Label: label, StartedAt: now(), Gateway: g.BaseURL, Agent: target.Agent, Model: target.Model, CasesSHA256: casesDigest, Cases: cases}
	if target.Prompt != "" {
		prompt := target.Prompt + RuntimeAddendum
		tokens := target.MaxOutputTokens
		if tokens <= 0 {
			tokens = 2048
		}
		agent, model, err := g.InstallPrompt(ctx, prompt, tokens)
		if err != nil {
			return nil, err
		}
		r.Agent, r.PromptSHA256 = agent, Digest([]byte(prompt))
		if r.Model == "" {
			r.Model = model
		}
	}
	if r.Agent == "" {
		return nil, fmt.Errorf("a prompt or an agent is required")
	}
	for i, c := range cases {
		msg := Message(c)
		job, err := g.Ask(ctx, r.Agent, msg)
		if err != nil {
			return nil, fmt.Errorf("case %s: %w", c.ID, err)
		}
		r.Results = append(r.Results, Result{CaseID: c.ID, Kind: c.Kind, Title: c.Title, Message: msg, Job: job, Score: ScoreReply(c, job.Reply)})
		if progress != nil {
			progress(i+1, len(cases))
		}
	}
	r.FinishedAt = now()
	r.seal()
	return r, nil
}

func (r *Run) seal() {
	r.Summary = Summary{Total: len(r.Results)}
	latency := 0
	for _, res := range r.Results {
		if res.Pass {
			r.Summary.Passed++
		}
		r.Summary.CitationsReturned += res.CitationsReturned
		r.Summary.CitationsAnchored += res.CitationsAnchored
		latency += res.LatencyMS
	}
	if len(r.Results) > 0 {
		r.Summary.MeanLatencyMS = latency / len(r.Results)
	}
	r.Digest = ""
	r.Digest = digestJSON(r)
}

// Verify re-scores every stored reply and checks the run digest, so an edited
// reply, score or summary is detected.
func Verify(r *Run) error {
	if r.EvaluatorVersion != EvaluatorVersion {
		return fmt.Errorf("run was scored by %s; this build scores with %s", r.EvaluatorVersion, EvaluatorVersion)
	}
	if len(r.Results) != len(r.Cases) {
		return fmt.Errorf("run has %d results for %d cases", len(r.Results), len(r.Cases))
	}
	for i, res := range r.Results {
		c := r.Cases[i]
		if res.CaseID != c.ID || res.Message != Message(c) {
			return fmt.Errorf("result %d is not bound to case %s", i, c.ID)
		}
		if digestJSON(ScoreReply(c, res.Reply)) != digestJSON(res.Score) {
			return fmt.Errorf("case %s: stored score differs from its reply", c.ID)
		}
	}
	claimed := r.Digest
	copy := *r
	copy.seal()
	if copy.Summary != r.Summary {
		return fmt.Errorf("run summary differs from its results")
	}
	if copy.Digest != claimed {
		return fmt.Errorf("run digest differs from its content")
	}
	return nil
}

// Load reads a run record.
func Load(path string) (*Run, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

// Save writes a run record.
func Save(r *Run, path string) error {
	raw, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

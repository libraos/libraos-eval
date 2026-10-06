package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RuntimeAddendum is appended to every prompt this tool installs. It states
// the no-tools contract so a model does not answer with tool-call text.
const RuntimeAddendum = `

## Runtime note (libraos-eval)

This agent is installed with tool_scope: none. You have no tools, search,
browsing, file or network access. Never write tool-call markup such as
<tool_call> or function calls: it cannot run and leaves the user without an
answer. Answer from the supplied material first, then list the specific evidence
you would ask the team to obtain.
`

// Gateway is a LibraOS gateway client for agent installation and jobs.
type Gateway struct {
	BaseURL      string
	Token        string
	PollInterval time.Duration
	HTTP         *http.Client
}

// NewGateway refuses non-loopback gateways unless allowRemote is set: case
// evidence is sent to the gateway and its model provider.
func NewGateway(base, token string, allowRemote bool) (*Gateway, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("gateway must be an http(s) URL")
	}
	if ip := net.ParseIP(u.Hostname()); !allowRemote && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("gateway %s is not a loopback address; pass --allow-remote to send case evidence to it", u.Host)
	}
	if token == "" {
		return nil, fmt.Errorf("a gateway token is required (LIBRAOS_TOKEN)")
	}
	return &Gateway{BaseURL: strings.TrimRight(base, "/"), Token: token, PollInterval: 2 * time.Second, HTTP: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (g *Gateway) do(ctx context.Context, method, path string, body io.Reader, headers map[string]string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, g.BaseURL+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err == nil && out != nil && len(data) > 0 && resp.StatusCode < 300 {
		err = json.Unmarshal(data, out)
	}
	return resp.StatusCode, err
}

func jsonBody(v any) io.Reader {
	raw, _ := json.Marshal(v)
	return bytes.NewReader(raw)
}

// InstallPrompt installs a prompt as a tool-free persona agent named after the
// exact prompt bytes, so one agent name always means one prompt.
func (g *Gateway) InstallPrompt(ctx context.Context, prompt string, maxOutputTokens int) (agent, model string, err error) {
	agent = "eval-" + Digest([]byte(prompt))[:24]
	var created struct {
		Model string `json:"model"`
	}
	body := map[string]any{"name": agent, "system": prompt, "agent_type": "persona", "brain": false, "tool_scope": "none", "triage": map[string]string{"default_brain": "never"}, "max_turns": 8, "max_output_tokens": maxOutputTokens, "capabilities": []string{}}
	code, err := g.do(ctx, http.MethodPost, "/v1/agents", jsonBody(body), map[string]string{"anthropic-beta": "managed-agents-2026-04-01"}, &created)
	if err != nil && code == 0 {
		return "", "", err
	}
	if code != http.StatusOK && code != http.StatusCreated && code != http.StatusConflict {
		return "", "", fmt.Errorf("gateway refused agent installation: HTTP %d", code)
	}
	return agent, created.Model, nil
}

// Job is the terminal state of one submitted message.
type Job struct {
	Ref        string `json:"ref"`
	Reply      string `json:"reply"`
	LatencyMS  int    `json:"latency_ms"`
	TokensUsed int    `json:"tokens_used,omitempty"`
}

// Ask submits one message once and polls until the job is terminal. It never
// resubmits: an unknown submission or failed job is returned as an error.
func (g *Gateway) Ask(ctx context.Context, agent, message string) (Job, error) {
	start := time.Now()
	var job struct {
		JobID      string  `json:"job_id"`
		Status     string  `json:"status"`
		Result     *string `json:"result"`
		Error      string  `json:"error"`
		TokensUsed int     `json:"tokens_used"`
	}
	path := "/agents/v1/" + url.PathEscape(agent) + "/jobs"
	code, err := g.do(ctx, http.MethodPost, path, jsonBody(map[string]any{"messages": []map[string]string{{"role": "user", "content": message}}}), nil, &job)
	if err != nil || code != http.StatusAccepted || job.JobID == "" {
		return Job{}, fmt.Errorf("job submission not accepted (HTTP %d): %v", code, err)
	}
	out := Job{Ref: agent + ":" + job.JobID}
	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(g.PollInterval):
		}
		if code, err = g.do(ctx, http.MethodGet, path+"/"+url.PathEscape(job.JobID), nil, nil, &job); err != nil || code != http.StatusOK {
			continue
		}
		switch job.Status {
		case "done":
			if job.Result == nil {
				return out, errors.New("job finished without a result")
			}
			out.Reply, out.LatencyMS, out.TokensUsed = *job.Result, int(time.Since(start).Milliseconds()), job.TokensUsed
			return out, nil
		case "failed", "cancelled":
			return out, fmt.Errorf("job %s: %s", job.Status, job.Error)
		}
	}
}

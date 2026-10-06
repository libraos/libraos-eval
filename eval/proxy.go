package eval

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"time"
)

// ProxyRecord is what the recording proxy logs for each model request: enough
// to prove which model was called and how many tool definitions it was given,
// without storing prompt content.
type ProxyRecord struct {
	At        string   `json:"at"`
	Method    string   `json:"method"`
	Path      string   `json:"path"`
	Model     string   `json:"model,omitempty"`
	Messages  int      `json:"messages"`
	Tools     int      `json:"tools"`
	ToolNames []string `json:"tool_names,omitempty"`
	Functions int      `json:"functions"`
	Stream    bool     `json:"stream"`
}

// RecordingProxy forwards OpenAI-compatible requests from a gateway to its
// model server and appends one ProxyRecord per request to a JSONL log.
func RecordingProxy(upstream, logPath string) (http.Handler, error) {
	target, err := url.Parse(upstream)
	if err != nil {
		return nil, err
	}
	forward := httputil.NewSingleHostReverseProxy(target)
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		r.Body = io.NopCloser(bytes.NewReader(body))
		rec := ProxyRecord{At: time.Now().UTC().Format(time.RFC3339Nano), Method: r.Method, Path: r.URL.Path}
		var req struct {
			Model     string            `json:"model"`
			Messages  []json.RawMessage `json:"messages"`
			Stream    bool              `json:"stream"`
			Functions []json.RawMessage `json:"functions"`
			Tools     []struct {
				Name     string `json:"name"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if json.Unmarshal(body, &req) == nil {
			rec.Model, rec.Messages, rec.Stream, rec.Tools, rec.Functions = req.Model, len(req.Messages), req.Stream, len(req.Tools), len(req.Functions)
			for _, t := range req.Tools {
				name := t.Function.Name
				if name == "" {
					name = t.Name
				}
				rec.ToolNames = append(rec.ToolNames, name)
			}
		}
		line, _ := json.Marshal(rec)
		mu.Lock()
		if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = f.Write(append(line, '\n'))
			_ = f.Close()
		}
		mu.Unlock()
		r.Host = target.Host
		forward.ServeHTTP(w, r)
	}), nil
}

// ToolExposure summarizes a proxy log: total requests, models seen and the
// largest number of tool definitions any request carried.
func ToolExposure(logPath string) (requests, maxTools int, models []string, err error) {
	raw, err := os.ReadFile(logPath)
	if err != nil {
		return 0, 0, nil, err
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		var rec ProxyRecord
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &rec) != nil || rec.Method != http.MethodPost {
			continue
		}
		requests++
		if n := rec.Tools + rec.Functions; n > maxTools {
			maxTools = n
		}
		if rec.Model != "" && !seen[rec.Model] {
			seen[rec.Model] = true
			models = append(models, rec.Model)
		}
	}
	return requests, maxTools, models, nil
}

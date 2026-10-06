package eval

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Employee describes one digital employee to install: the LibraOS employee
// record and the tool-free persona agent bound to it with owner_employee.
type Employee struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name"`
	Description     string `json:"description"`
	AgentID         string `json:"agent_id"`
	Prompt          string `json:"-"`
	Model           string `json:"model,omitempty"`
	MaxTurns        int    `json:"max_turns,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
}

var slug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Bundle builds a LibraOS employee bundle (schema_version 1). The agent body is
// the prompt followed by RuntimeAddendum.
func (e Employee) Bundle() ([]byte, string, error) {
	if !slug.MatchString(e.ID) || !slug.MatchString(e.AgentID) || e.ID == e.AgentID {
		return nil, "", fmt.Errorf("employee and agent ids must be distinct kebab-case slugs")
	}
	if strings.TrimSpace(e.Prompt) == "" || strings.ContainsAny(e.DisplayName+e.Description, "\n") {
		return nil, "", fmt.Errorf("a prompt and single-line display name and description are required")
	}
	turns, tokens := e.MaxTurns, e.MaxOutputTokens
	if turns <= 0 {
		turns = 8
	}
	if tokens <= 0 {
		tokens = 2048
	}
	header := []string{"---", "name: " + e.AgentID, "description: " + e.Description, "agent_type: persona", "brain: false", "owner_employee: " + e.ID}
	if e.Model != "" {
		header = append(header, "model: "+e.Model)
	}
	header = append(header, "tool_scope: none", "triage:", "  default_brain: never", fmt.Sprintf("max_turns: %d", turns), fmt.Sprintf("max_output_tokens: %d", tokens), "---", "")
	agent := strings.Join(header, "\n") + strings.TrimSpace(e.Prompt) + "\n" + RuntimeAddendum
	employee := fmt.Sprintf("---\nid: %s\ndisplay_name: %s\ndescription: %s\n---\n", e.ID, e.DisplayName, e.Description)
	manifest, _ := json.Marshal(map[string]any{"schema_version": 1, "employee_id": e.ID, "nova_os_min_version": "v0.2.0", "created_at": time.Now().UTC().Format(time.RFC3339), "agents": []string{e.AgentID}})
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, content := range map[string]string{"manifest.json": string(manifest), "employee.md": employee, "agents/" + e.AgentID + ".md": agent} {
		w, err := z.Create(name)
		if err != nil {
			return nil, "", err
		}
		if _, err = w.Write([]byte(content)); err != nil {
			return nil, "", err
		}
	}
	if err := z.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), Digest([]byte(agent)), nil
}

// InstallEmployee imports the bundle (overwriting an existing employee) and
// reloads agents. It returns the installed agent definition's digest.
func (g *Gateway) InstallEmployee(ctx context.Context, e Employee) (string, error) {
	bundle, agentDigest, err := e.Bundle()
	if err != nil {
		return "", err
	}
	code, err := g.do(ctx, http.MethodPost, "/v1/managed/employees/bundle/import?on_conflict=overwrite", bytes.NewReader(bundle), map[string]string{"Content-Type": "application/zip"}, nil)
	if err != nil || code != http.StatusOK {
		return "", fmt.Errorf("employee bundle import failed (HTTP %d): %v", code, err)
	}
	var reload struct {
		Errors []any `json:"errors"`
	}
	code, err = g.do(ctx, http.MethodPost, "/api/admin/agents/reload", nil, map[string]string{"nova-beta": "agents-reload-2026-04-01"}, &reload)
	if err != nil || code != http.StatusOK || len(reload.Errors) > 0 {
		return "", fmt.Errorf("agent reload failed (HTTP %d): %v %v", code, err, reload.Errors)
	}
	return agentDigest, nil
}

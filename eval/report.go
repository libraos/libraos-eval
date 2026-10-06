package eval

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed report.html.tmpl
var reportFiles embed.FS

var reportTemplate = template.Must(template.New("report.html.tmpl").Funcs(template.FuncMap{
	"pct": func(a, b int) string {
		if b == 0 {
			return "–"
		}
		return fmt.Sprintf("%.0f%%", 100*float64(a)/float64(b))
	},
	"mul100": func(f float64) float64 { return 100 * f },
	"short": func(s string) string {
		if len(s) > 12 {
			return s[:12]
		}
		return s
	},
}).ParseFS(reportFiles, "report.html.tmpl"))

// RunView pairs a run with its file name and verification outcome.
type RunView struct {
	File     string
	Run      *Run
	Verified string
}

type page struct {
	Title   string
	Runs    []RunView
	Detail  *RunView
	Compare *Comparison
	Error   string
	Served  bool
}

func view(file string, r *Run) RunView {
	v := RunView{File: file, Run: r, Verified: "verified"}
	if err := Verify(r); err != nil {
		v.Verified = err.Error()
	}
	return v
}

// WriteReport renders a self-contained HTML report for runs; with exactly two
// comparable runs it also shows their comparison.
func WriteReport(w io.Writer, files []string) error {
	p := page{Title: "libraos-eval report"}
	var runs []*Run
	for _, f := range files {
		r, err := Load(f)
		if err != nil {
			return err
		}
		runs = append(runs, r)
		p.Runs = append(p.Runs, view(filepath.Base(f), r))
	}
	if len(p.Runs) == 1 {
		p.Detail = &p.Runs[0]
	}
	if len(runs) == 2 {
		if c, err := Compare(runs[0], runs[1]); err == nil {
			p.Compare = c
		}
	}
	return reportTemplate.Execute(w, p)
}

// Serve is a read-only local UI over a directory of run records. It binds to
// loopback only.
func Serve(addr, dir string) error {
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("serve binds to a loopback address only, got %q", addr)
	}
	load := func() []RunView {
		entries, _ := os.ReadDir(dir)
		var out []RunView
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			if r, err := Load(filepath.Join(dir, e.Name())); err == nil && r.Digest != "" {
				out = append(out, view(e.Name(), r))
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Run.StartedAt > out[j].Run.StartedAt })
		return out
	}
	find := func(runs []RunView, name string) *RunView {
		for i := range runs {
			if runs[i].File == name {
				return &runs[i]
			}
		}
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		runs := load()
		p := page{Title: "libraos-eval", Runs: runs, Served: true}
		if f := r.URL.Query().Get("run"); f != "" {
			p.Detail = find(runs, f)
		}
		if a, b := r.URL.Query().Get("a"), r.URL.Query().Get("b"); a != "" && b != "" {
			if ra, rb := find(runs, a), find(runs, b); ra != nil && rb != nil {
				if c, err := Compare(ra.Run, rb.Run); err == nil {
					p.Compare = c
				} else {
					p.Error = err.Error()
				}
			}
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		_ = reportTemplate.Execute(w, p)
	})
	return http.ListenAndServe(addr, mux)
}

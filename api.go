package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var apiClient = &http.Client{Timeout: 30 * time.Second}

type workload struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProjectName  string `json:"projectName"`
	Phase        string `json:"phase"`
	Environments []struct {
		Connections []connection `json:"connections"`
	} `json:"environments"`
}

type connection struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// sshURLs gives the URL of the Workspace's connection named "SSH..." (the template names it "SSH (client only)"),
// with named true. When every connection is named templt-<n> it gives all of them, with named false: the Run:ai UI
// (3.0.20) loses the names when the template is edited in advanced setup, and the SSH one is then the one whose
// /user answers (findSSH).
func (w workload) sshURLs() (urls []string, named bool) {
	unnamed := true
	for _, e := range w.Environments {
		for _, c := range e.Connections {
			if strings.HasPrefix(c.Name, "SSH") {
				return []string{c.URL}, true
			}
			unnamed = unnamed && strings.HasPrefix(c.Name, "templt-")
			urls = append(urls, c.URL)
		}
	}
	if !unnamed {
		return nil, false
	}
	return urls, false
}

// workspaces lists the Workspaces you submitted that have an SSH connection, all of them or those named name.
// Stopped Workspaces keep their connections, so they are listed too.
func workspaces(e env, tok, email, name string) ([]workload, error) {
	// In filterBy, a comma means AND.
	f := "type==Workspace,submittedBy==" + email
	if name != "" {
		f += ",name==" + name
	}
	q := url.Values{"filterBy": {f}, "limit": {"500"}}
	var out struct {
		Workloads []workload `json:"workloads"`
	}
	if err := call(e, tok, http.MethodGet, "/api/v1/workloads?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	var ws []workload
	for _, w := range out.Workloads {
		if urls, _ := w.sshURLs(); len(urls) > 0 {
			ws = append(ws, w)
		}
	}
	return ws, nil
}

func resume(e env, tok, id string) error {
	return call(e, tok, http.MethodPost, "/api/v1/workloads/workspaces/"+id+"/resume", nil)
}

func call(e env, tok, method, path string, out any) error {
	req, err := http.NewRequest(method, e.apiBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("can't reach the Run:ai API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("Run:ai API %s %s: %s %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

package forge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ADOClient queries Azure DevOps Git repositories.
// orgURL is the full org URL, e.g. https://dev.azure.com/bossinfo.
// Auth uses HTTP Basic with empty username + PAT.
type ADOClient struct {
	token  string
	orgURL string
	http   *http.Client
}

func NewADOClient(token, orgURL string) *ADOClient {
	return &ADOClient{token: token, orgURL: strings.TrimRight(orgURL, "/"), http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *ADOClient) Name() string { return "ado" }

func (c *ADOClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.orgURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(":" + c.token))
		req.Header.Set("Authorization", "Basic "+cred)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ado %s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type adoProject struct {
	Name string `json:"name"`
}

type adoRepo struct {
	Name          string     `json:"name"`
	Project       adoProject `json:"project"`
	RemoteURL     string     `json:"remoteUrl"`
	SSHURL        string     `json:"sshUrl"`
	DefaultBranch string     `json:"defaultBranch"`
}

type adoRepoList struct {
	Value []adoRepo `json:"value"`
}

func (c *ADOClient) ListRepos(ctx context.Context, _ string) ([]RepoRef, error) {
	var raw adoRepoList
	if err := c.get(ctx, "/_apis/git/repositories?api-version=7.1", &raw); err != nil {
		return nil, err
	}
	out := make([]RepoRef, 0, len(raw.Value))
	for _, r := range raw.Value {
		branch := strings.TrimPrefix(r.DefaultBranch, "refs/heads/")
		out = append(out, RepoRef{
			Forge:         "ado",
			Owner:         r.Project.Name,
			Name:          r.Name,
			DefaultBranch: branch,
			HTMLURL:       r.RemoteURL,
			SSHURL:        r.SSHURL,
		})
	}
	return out, nil
}

// ADO work items are not GitHub-style issues; return empty to keep the Client interface.
func (c *ADOClient) ListOpenIssues(_ context.Context, _, _ string) ([]Issue, error) {
	return nil, nil
}

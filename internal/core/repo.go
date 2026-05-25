package core

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Repo is a discovered local repository.
type Repo struct {
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	Forge         string    `json:"forge"`
	Owner         string    `json:"owner"`
	Visibility    string    `json:"visibility"`
	Topics        []string  `json:"topics,omitempty"`
	Desc          string    `json:"desc,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	RemoteURL     string    `json:"remote_url,omitempty"`
	LastUsed      time.Time `json:"last_used,omitempty"`
}

// DiscoverRepos walks root and returns repos:
//
//	github/<owner>/(public|private)/<repo>
//	gitlab/<owner>/<repo>
//	git-forgejo/<repo>
func DiscoverRepos(root string) ([]Repo, error) {
	var out []Repo
	walkGithub := func(forgeDir string) error {
		owners, err := os.ReadDir(forgeDir)
		if err != nil {
			return err
		}
		for _, owner := range owners {
			if !owner.IsDir() {
				continue
			}
			for _, vis := range []string{"public", "private"} {
				visDir := filepath.Join(forgeDir, owner.Name(), vis)
				repos, err := os.ReadDir(visDir)
				if err != nil {
					continue
				}
				for _, r := range repos {
					if !r.IsDir() {
						continue
					}
					out = append(out, Repo{
						Name:       r.Name(),
						Path:       filepath.Join(visDir, r.Name()),
						Forge:      "github",
						Owner:      owner.Name(),
						Visibility: vis,
					})
				}
			}
		}
		return nil
	}
	walkGitlab := func(forgeDir string) error {
		owners, err := os.ReadDir(forgeDir)
		if err != nil {
			return err
		}
		for _, owner := range owners {
			if !owner.IsDir() {
				continue
			}
			ownerDir := filepath.Join(forgeDir, owner.Name())
			repos, err := os.ReadDir(ownerDir)
			if err != nil {
				continue
			}
			for _, r := range repos {
				if !r.IsDir() {
					continue
				}
				if strings.HasPrefix(r.Name(), ".") {
					continue
				}
				out = append(out, Repo{
					Name:  r.Name(),
					Path:  filepath.Join(ownerDir, r.Name()),
					Forge: "gitlab",
					Owner: owner.Name(),
				})
			}
		}
		return nil
	}
	walkForgejo := func(forgeDir string) error {
		repos, err := os.ReadDir(forgeDir)
		if err != nil {
			return err
		}
		for _, r := range repos {
			if !r.IsDir() {
				continue
			}
			if strings.HasPrefix(r.Name(), ".") {
				continue
			}
			out = append(out, Repo{
				Name:  r.Name(),
				Path:  filepath.Join(forgeDir, r.Name()),
				Forge: "forgejo",
				Owner: "freax",
			})
		}
		return nil
	}

	if d := filepath.Join(root, "github"); dirExists(d) {
		if err := walkGithub(d); err != nil {
			return nil, err
		}
	}
	if d := filepath.Join(root, "gitlab"); dirExists(d) {
		if err := walkGitlab(d); err != nil {
			return nil, err
		}
	}
	if d := filepath.Join(root, "git-forgejo"); dirExists(d) {
		if err := walkForgejo(d); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

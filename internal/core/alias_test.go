package core

import (
	"errors"
	"testing"
)

func TestResolveAlias(t *testing.T) {
	repos := []Repo{
		{Name: "bridge", Forge: "github", Owner: "freaxnx01", Alias: "br"},
		{Name: "agent-pipeline", Forge: "github", Owner: "freaxnx01", Alias: "agp"},
		{Name: "no-alias", Forge: "forgejo", Owner: "freax"},
	}

	t.Run("hit", func(t *testing.T) {
		got, err := ResolveAlias("br", repos)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got.Name != "bridge" || got.Forge != "github" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("case_insensitive", func(t *testing.T) {
		got, err := ResolveAlias("AGP", repos)
		if err != nil || got.Name != "agent-pipeline" {
			t.Fatalf("got %+v err %v", got, err)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		_, err := ResolveAlias("nope", repos)
		if !errors.Is(err, ErrAliasNotFound) {
			t.Fatalf("err = %v, want ErrAliasNotFound", err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		_, err := ResolveAlias("", repos)
		if !errors.Is(err, ErrAliasNotFound) {
			t.Fatalf("err = %v, want ErrAliasNotFound", err)
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		dup := append([]Repo{}, repos...)
		dup = append(dup, Repo{Name: "brdup", Forge: "forgejo", Owner: "freax", Alias: "br"})
		_, err := ResolveAlias("br", dup)
		if !errors.Is(err, ErrAliasAmbiguous) {
			t.Fatalf("err = %v, want ErrAliasAmbiguous", err)
		}
	})
}

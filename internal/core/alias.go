package core

import (
	"errors"
	"strings"
)

var (
	// ErrAliasNotFound is returned when no repo carries the requested alias.
	ErrAliasNotFound = errors.New("unknown alias")
	// ErrAliasAmbiguous is returned when more than one repo carries the alias;
	// bridge never silently picks one.
	ErrAliasAmbiguous = errors.New("ambiguous alias")
)

// ResolveAlias maps a repo alias to the single repo that declares it. Matching
// is case-insensitive on Repo.Alias. A blank alias, or an alias no repo carries,
// yields ErrAliasNotFound; two or more carriers yield ErrAliasAmbiguous.
func ResolveAlias(alias string, repos []Repo) (Repo, error) {
	want := strings.ToLower(strings.TrimSpace(alias))
	if want == "" {
		return Repo{}, ErrAliasNotFound
	}
	var match Repo
	found := 0
	for _, r := range repos {
		if r.Alias != "" && strings.EqualFold(r.Alias, want) {
			match = r
			found++
		}
	}
	switch found {
	case 0:
		return Repo{}, ErrAliasNotFound
	case 1:
		return match, nil
	default:
		return Repo{}, ErrAliasAmbiguous
	}
}

package herdr

import (
	"regexp"
	"testing"
)

var legalName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func TestAgentName_SanitizesToALegalHerdrName(t *testing.T) {
	tests := []struct {
		name  string
		slot  string
		taken []string
		want  string
	}{
		{"already legal", "bridge", nil, "bridge"},
		{"uppercase is lowered", "Avaloq", nil, "avaloq"},
		{"underscores survive", "BI_ExportSQLiteToCsv", nil, "bi_exportsqlitetocsv"},
		{"worktree slot", "bridge-wt-foo", nil, "bridge-wt-foo"},
		{"dots become dashes", "my.repo.name", nil, "my-repo-name"},
		{"leading digit gets a prefix", "3d-engine", nil, "a-3d-engine"},
		{"runs of separators collapse", "a..--b", nil, "a-b"},
		{"trailing separators are trimmed", "repo--", nil, "repo"},
		{
			"over 32 chars is truncated",
			"quilvest-archiverestapi-wt-featurebranch",
			nil,
			"quilvest-archiverestapi-wt-featu",
		},
		{
			"collision gets a numeric suffix",
			"bridge",
			[]string{"bridge"},
			"bridge-2",
		},
		{
			"repeated collisions keep counting",
			"bridge",
			[]string{"bridge", "bridge-2"},
			"bridge-3",
		},
		{
			"a suffix on a max-length name still fits in 32",
			"quilvest-archiverestapi-wt-featurebranch",
			[]string{"quilvest-archiverestapi-wt-featu"},
			"quilvest-archiverestapi-wt-fea-2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentName(tt.slot, tt.taken)
			if got != tt.want {
				t.Errorf("agentName(%q, %v) = %q, want %q", tt.slot, tt.taken, got, tt.want)
			}
			if !legalName.MatchString(got) {
				t.Errorf("%q is not a legal herdr agent name", got)
			}
		})
	}
}

func TestAgentName_EmptySlot_StillProducesALegalName(t *testing.T) {
	got := agentName("", nil)
	if !legalName.MatchString(got) {
		t.Errorf("agentName(\"\") = %q, which is not a legal herdr agent name", got)
	}
}

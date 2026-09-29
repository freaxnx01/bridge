package gitauth_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/gitauth"
)

func TestCredentialHelper_ADO_ReadsPATEnv(t *testing.T) {
	h := gitauth.CredentialHelper("ado")
	if !strings.HasPrefix(h, "credential.https://dev.azure.com.helper=") {
		t.Errorf("ado helper missing prefix: %q", h)
	}
	if !strings.Contains(h, "AZURE_DEVOPS_EXT_PAT") || !strings.Contains(h, "ADO_PAT") {
		t.Errorf("ado helper should reference both env vars: %q", h)
	}
}

func TestCredentialHelper_Github_ReadsTokenEnv(t *testing.T) {
	h := gitauth.CredentialHelper("github")
	if !strings.HasPrefix(h, "credential.https://github.com.helper=") {
		t.Errorf("github helper missing prefix: %q", h)
	}
	if !strings.Contains(h, "GH_TOKEN") || !strings.Contains(h, "GITHUB_TOKEN") {
		t.Errorf("github helper should reference both env vars: %q", h)
	}
}

func TestCredentialHelper_Forgejo_ReadsTokenEnv(t *testing.T) {
	h := gitauth.CredentialHelper("forgejo")
	if !strings.HasPrefix(h, "credential.helper=") {
		t.Errorf("forgejo helper should be unscoped (host varies per install): %q", h)
	}
	if !strings.Contains(h, "FORGEJO_TOKEN") {
		t.Errorf("forgejo helper should reference FORGEJO_TOKEN: %q", h)
	}
}

func TestCredentialHelper_OtherForges_Empty(t *testing.T) {
	if gitauth.CredentialHelper("gitlab") != "" {
		t.Error("gitlab should return empty (no helper)")
	}
}

func TestCredentialArgs_Github_AppendsScopedHelper(t *testing.T) {
	want := []string{"-c", gitauth.CredentialHelper("github")}
	if got := gitauth.CredentialArgs("github"); !reflect.DeepEqual(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCredentialArgs_Forgejo_ResetsHelpersBeforeTokenHelper(t *testing.T) {
	want := []string{"-c", "credential.helper=", "-c", gitauth.CredentialHelper("forgejo")}
	if got := gitauth.CredentialArgs("forgejo"); !reflect.DeepEqual(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCredentialArgs_ForgeWithoutHelper_Nil(t *testing.T) {
	if got := gitauth.CredentialArgs("gitlab"); got != nil {
		t.Errorf("args = %q, want nil", got)
	}
}

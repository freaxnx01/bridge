package gitauth_test

import (
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

func TestCredentialHelper_OtherForges_Empty(t *testing.T) {
	if gitauth.CredentialHelper("gitlab") != "" || gitauth.CredentialHelper("forgejo") != "" {
		t.Error("non-github/non-ado should return empty (no helper)")
	}
}

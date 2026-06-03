// Package gitauth wires forge credentials into git invocations without ever
// persisting tokens. Helpers read the PAT/token from the environment (typically
// injected by direnv from a repo's .envrc) at credential-prompt time, so secrets
// never land in .git/config or a process command line.
package gitauth

// CredentialHelper returns a git `-c` value wiring an inline credential helper
// for the given forge. The helper reads the PAT/token from the environment at
// credential-prompt time (so it is never persisted in .git/config or visible in
// /proc command-line listings). Empty string means the forge needs no helper
// (plain git).
func CredentialHelper(forge string) string {
	switch forge {
	case "ado":
		return `credential.https://dev.azure.com.helper=!f() { echo username=x; echo "password=${AZURE_DEVOPS_EXT_PAT:-$ADO_PAT}"; }; f`
	case "github":
		return `credential.https://github.com.helper=!f() { echo username=x-access-token; echo "password=${GH_TOKEN:-$GITHUB_TOKEN}"; }; f`
	}
	return ""
}

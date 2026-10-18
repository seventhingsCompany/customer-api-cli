package installer

import (
	"os"
	"regexp"
	"testing"
)

func TestInstallerCertificateIdentity(t *testing.T) {
	script, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`--certificate-identity-regexp '([^']+)'`).FindSubmatch(script)
	if len(match) != 2 {
		t.Fatal("installer must verify the certificate identity")
	}
	identity, err := regexp.Compile(string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, subject string
		want          bool
	}{
		{"release", "https://github.com/seventhingsCompany/customer-api-cli/.github/workflows/release.yml@refs/tags/v0.2.1", true},
		{"existing release", "https://github.com/seventhingsCompany/customer-api-cli/.github/workflows/release.yml@refs/tags/v0.1.0", true},
		{"other organization", "https://github.com/other/customer-api-cli/.github/workflows/release.yml@refs/tags/v0.2.1", false},
		{"other repository", "https://github.com/seventhingsCompany/other/.github/workflows/release.yml@refs/tags/v0.2.1", false},
		{"repository prefix", "https://github.com/seventhingsCompany/customer-api-cli-other/.github/workflows/release.yml@refs/tags/v0.2.1", false},
		{"untrusted prefix", "https://example.com/https://github.com/seventhingsCompany/customer-api-cli/.github/workflows/release.yml@refs/tags/v0.2.1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := identity.MatchString(tc.subject); got != tc.want {
				t.Errorf("certificate identity matches %q = %v, want %v", tc.subject, got, tc.want)
			}
		})
	}
}

package dash

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// awsEncode renders a policy document in CloudFront's own base64 alphabet.
func awsEncode(document string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(document))
	return strings.NewReplacer("+", "-", "_", "/", "~", "=").Replace(encoded)
}

const testPolicyDocument = `{"Statement":[{"Resource":"https://cdn.test/output/*","Condition":{"DateLessThan":{"AWS:EpochTime":1792383997}}}]}`

func testCloudFrontQuery() string {
	return "CloudFront-Key-Pair-Id=K2TD0NY2KZ4IHD" +
		"&CloudFront-Policy=" + awsEncode(testPolicyDocument) +
		"&CloudFront-Signature=sig-a+b/c~d"
}

func TestCloudFrontCookieHeader(t *testing.T) {
	header := CloudFrontCookieHeader(testCloudFrontQuery())
	for _, want := range []string{"CloudFront-Policy=", "CloudFront-Signature=sig-a+b/c~d", "CloudFront-Key-Pair-Id=K2TD0NY2KZ4IHD"} {
		if !strings.Contains(header, want) {
			t.Fatalf("cookie header %q misses %q", header, want)
		}
	}
	// Values must stay byte-identical: no percent-decoding on the way.
	if strings.Contains(header, "%") {
		t.Fatalf("cookie header re-encoded a value: %q", header)
	}

	for _, incomplete := range []string{
		"",
		"CloudFront-Signature=sig&CloudFront-Key-Pair-Id=K1",
		"CloudFront-Policy=" + awsEncode(testPolicyDocument) + "&CloudFront-Key-Pair-Id=K1",
		"token=abc",
	} {
		if got := CloudFrontCookieHeader(incomplete); got != "" {
			t.Fatalf("CloudFrontCookieHeader(%q) = %q, want no cookie header", incomplete, got)
		}
	}
}

func TestCloudFrontPolicy(t *testing.T) {
	resource, expires, ok := CloudFrontPolicy(testCloudFrontQuery())
	if !ok {
		t.Fatal("policy was not decoded")
	}
	if resource != "https://cdn.test/output/*" {
		t.Fatalf("resource = %q", resource)
	}
	if want := time.Unix(1792383997, 0); !expires.Equal(want) {
		t.Fatalf("expires = %s, want %s", expires, want)
	}

	if _, _, ok := CloudFrontPolicy("token=abc"); ok {
		t.Fatal("a non-CloudFront query must not decode")
	}
}

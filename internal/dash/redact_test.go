package dash

import (
	"strings"
	"testing"
)

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"without query", "https://cdn.test/a.m4s", "https://cdn.test/a.m4s"},
		{"key value pairs", "https://cdn.test/a.m4s?exp=1&token=secret", "https://cdn.test/a.m4s?exp=…&token=…"},
		{"bare token", "https://cdn.test/a.m4s?AbCdEf123", "https://cdn.test/a.m4s?…"},
		{"over-long key", "https://cdn.test/a.m4s?" + "x0123456789012345678901234567890123", "https://cdn.test/a.m4s?…"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactURL(tc.in); got != tc.want {
				t.Fatalf("RedactURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactURLKeepsNonQueryParts(t *testing.T) {
	got := RedactURL("https://cdn.test/output/ardplus/ReCom_1/seg-1.m4s?sig=abcdef")
	if want := "https://cdn.test/output/ardplus/ReCom_1/seg-1.m4s?sig=…"; got != want {
		t.Fatalf("RedactURL = %q, want %q", got, want)
	}
}

// CloudFront signatures are base64 values: appending must not re-encode them,
// otherwise the CDN rejects the request.
func TestAppendQueryKeepsSignatureVerbatim(t *testing.T) {
	const signature = "CloudFront-Signature=a+b/c=d~e_f-0"
	const policy = "CloudFront-Policy=eyJ+Sta/tement=="
	query := "CloudFront-Key-Pair-Id=K2TD&" + policy + "&" + signature
	got := AppendQuery("https://cdn.test/a.m4s", query)
	if want := "https://cdn.test/a.m4s?" + query; got != want {
		t.Fatalf("AppendQuery = %q, want %q", got, want)
	}
	if strings.Contains(got, "%2B") || strings.Contains(got, "%2F") || strings.Contains(got, "%3D") {
		t.Fatalf("the signature was re-encoded: %q", got)
	}
}

func TestAppendQueryKeepsOwnParameters(t *testing.T) {
	segURL := "https://cdn.test/a.m4s?own=1"
	if got := AppendQuery(segURL, "token=abc"); got != segURL {
		t.Fatalf("AppendQuery = %q, want the unchanged segment URL", got)
	}
	if got := AppendQuery("https://cdn.test/a.m4s", ""); got != "https://cdn.test/a.m4s" {
		t.Fatalf("AppendQuery without a manifest query = %q", got)
	}
}

func TestQueryKeysDescription(t *testing.T) {
	if got := QueryKeys(""); got != "(none)" {
		t.Fatalf("empty query = %q", got)
	}
	if got := QueryKeys("exp=1&token=abc"); got != "exp,token" {
		t.Fatalf("keys = %q", got)
	}
	if got := QueryKeys("AbCdEf123"); got != "(bare token)" {
		t.Fatalf("bare token = %q", got)
	}
}

func TestStatusErrorRedactsToken(t *testing.T) {
	err := &httpStatusError{URL: "https://cdn.test/a.m4s?AbCdEfSecret", Status: 403, Mode: queryModeAppended.String()}
	if got, want := err.Error(), "https://cdn.test/a.m4s?…: HTTP 403 [manifest-query]"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

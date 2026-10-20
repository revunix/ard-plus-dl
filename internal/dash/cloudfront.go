package dash

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// queryValue returns one raw parameter without decoding it: signatures must
// stay byte-identical.
func queryValue(rawQuery, key string) string {
	for _, part := range strings.Split(rawQuery, "&") {
		name, value, found := strings.Cut(part, "=")
		if found && name == key {
			return value
		}
	}
	return ""
}

// CloudFrontCookieHeader renders the CloudFront parameters of a manifest query
// as a cookie header. It returns "" unless all three parameters are present.
//
// ARD's media cache behaviour reads these values from cookies only: a query
// string with the very same parameters is answered with "MissingKey".
func CloudFrontCookieHeader(rawQuery string) string {
	policy := queryValue(rawQuery, "CloudFront-Policy")
	signature := queryValue(rawQuery, "CloudFront-Signature")
	keyPairID := queryValue(rawQuery, "CloudFront-Key-Pair-Id")
	if policy == "" || signature == "" || keyPairID == "" {
		return ""
	}
	return "CloudFront-Policy=" + policy +
		"; CloudFront-Signature=" + signature +
		"; CloudFront-Key-Pair-Id=" + keyPairID
}

type cloudFrontPolicyDocument struct {
	Statement []struct {
		Resource  string `json:"Resource"`
		Condition struct {
			DateLessThan struct {
				AWSEpochTime int64 `json:"AWS:EpochTime"`
			} `json:"DateLessThan"`
		} `json:"Condition"`
	} `json:"Statement"`
}

// CloudFrontPolicy decodes the CloudFront-Policy parameter of a query string
// into its resource pattern and expiry, for diagnostics only.
func CloudFrontPolicy(rawQuery string) (resource string, expires time.Time, ok bool) {
	encoded := queryValue(rawQuery, "CloudFront-Policy")
	if encoded == "" {
		return "", time.Time{}, false
	}
	standard := strings.NewReplacer("-", "+", "_", "/", "~", "=").Replace(encoded)
	data, err := base64.StdEncoding.DecodeString(standard)
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(standard, "=")); err != nil {
			return "", time.Time{}, false
		}
	}
	var document cloudFrontPolicyDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return "", time.Time{}, false
	}
	if len(document.Statement) == 0 {
		return "", time.Time{}, false
	}
	statement := document.Statement[0]
	return statement.Resource, time.Unix(statement.Condition.DateLessThan.AWSEpochTime, 0), true
}

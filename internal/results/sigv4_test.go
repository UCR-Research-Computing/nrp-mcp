package results

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Reference value computed independently in Python (hmac/hashlib) with the AWS rule that
// query values are URI-encoded, "/" included (prefix=runs%2Fr1%2F). botocore keeps "/"
// raw in the query and gets 2caeab6a...; both are accepted by S3 and Ceph RGW, which
// re-encode the same way the client did only when the client sent the encoded form, so
// SignRequest also sends the query encoded (see listURL). 2026-10-18.
func TestSignRequestMatchesReference(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://s3-west.nrp-nautilus.io/my-bucket?list-type=2&prefix=runs/r1/", nil)
	SignRequest(req, nil, "AK", "SK", time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AK/20261020/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=89d061ecf1725f74bcb78ef3cf4e944b8d1d2736a352362381df30bbc0ec3dfc"
	if a := req.Header.Get("Authorization"); a != want {
		t.Fatalf("authorization\n got %s\nwant %s", a, want)
	}
	if req.Header.Get("x-amz-content-sha256") != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatal("empty payload hash")
	}
}

func TestSignPostLimitsPrefix(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	f, err := SignPost("https://s3-west.nrp-nautilus.io", "nrp-results-abc", "runs/r1/", "AK", "SECRETKEY", 5<<30, now.Add(25*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if f.URL != "https://s3-west.nrp-nautilus.io/nrp-results-abc" || f.Prefix != "runs/r1/" {
		t.Fatalf("%+v", f)
	}
	pj, _ := base64.StdEncoding.DecodeString(f.Fields["policy"])
	for _, want := range []string{`"starts-with","$key","runs/r1/"`, `"bucket":"nrp-results-abc"`, `"expiration":"2026-10-21T13:00:00.000Z"`} {
		if !strings.Contains(string(pj), want) {
			t.Fatalf("policy %s lacks %s", pj, want)
		}
	}
	for k, v := range f.Fields {
		if strings.Contains(v, "SECRETKEY") {
			t.Fatalf("secret key leaked in field %s", k)
		}
	}
}

func TestSignRequestSendsSignedQuery(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://s3-west.nrp-nautilus.io/b?prefix=runs/r1/&list-type=2", nil)
	SignRequest(req, nil, "AK", "SK", time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	if req.URL.RawQuery != "list-type=2&prefix=runs%2Fr1%2F" {
		t.Fatalf("raw query %q", req.URL.RawQuery)
	}
}

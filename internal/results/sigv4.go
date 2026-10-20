// Package results keeps job output in the person's NRP S3 bucket. Pods never get S3
// keys: nrp_run signs a POST policy that can only write under runs/<run>/ until the job's
// deadline, and a small uploader sidecar sends /work/results there when the job ends.
// The laptop lists and downloads with the person's own keys (SigV4, no SDK).
package results

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Region is what Ceph RGW accepts for SigV4 (any value works; us-east-1 is the norm).
const Region = "us-east-1"

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func signingKey(secret, day string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), day)
	k = hmacSHA256(k, Region)
	k = hmacSHA256(k, "s3")
	return hmacSHA256(k, "aws4_request")
}

// uriEncode is AWS's URI encoding (RFC 3986 unreserved kept; "/" kept when path).
func uriEncode(s string, path bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' || (path && c == '/') {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// SignRequest adds SigV4 headers to req (path-style URL, payload already known).
func SignRequest(req *http.Request, payload []byte, access, secret string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	ph := sha256Hex(payload)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", ph)
	host := req.URL.Host
	q := req.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var qs []string
	for _, k := range keys {
		vs := q[k]
		sort.Strings(vs)
		for _, v := range vs {
			qs = append(qs, uriEncode(k, false)+"="+uriEncode(v, false))
		}
	}
	// Send exactly the query that is signed (Go would otherwise leave "/" unencoded).
	req.URL.RawQuery = strings.Join(qs, "&")
	signed := "host;x-amz-content-sha256;x-amz-date"
	canon := strings.Join([]string{req.Method, uriEncode(req.URL.Path, true), strings.Join(qs, "&"),
		"host:" + host + "\nx-amz-content-sha256:" + ph + "\nx-amz-date:" + amzDate + "\n", signed, ph}, "\n")
	scope := day + "/" + Region + "/s3/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canon))
	sig := hex.EncodeToString(hmacSHA256(signingKey(secret, day), sts))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+access+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

// PostForm is a signed S3 POST policy: the fields a pod sends with each upload.
type PostForm struct {
	URL    string            `json:"url"`
	Fields map[string]string `json:"fields"`
	Prefix string            `json:"prefix"`
}

// SignPost makes a POST policy that allows uploads only to keys under prefix, at most
// maxBytes each, until expires. The secret key never leaves this computer.
func SignPost(endpoint, bucket, prefix, access, secret string, maxBytes int64, expires, now time.Time) (*PostForm, error) {
	u, err := url.Parse(strings.TrimRight(endpoint, "/") + "/" + bucket)
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	day := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	cred := access + "/" + day + "/" + Region + "/s3/aws4_request"
	policy := map[string]any{
		"expiration": expires.UTC().Format("2006-01-02T15:04:05.000Z"),
		"conditions": []any{
			map[string]string{"bucket": bucket},
			[]any{"starts-with", "$key", prefix},
			[]any{"content-length-range", 0, maxBytes},
			map[string]string{"x-amz-algorithm": "AWS4-HMAC-SHA256"},
			map[string]string{"x-amz-credential": cred},
			map[string]string{"x-amz-date": amzDate},
		},
	}
	pj, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	p64 := base64.StdEncoding.EncodeToString(pj)
	sig := hex.EncodeToString(hmacSHA256(signingKey(secret, day), p64))
	return &PostForm{URL: u.String(), Prefix: prefix, Fields: map[string]string{
		"policy": p64, "x-amz-algorithm": "AWS4-HMAC-SHA256", "x-amz-credential": cred, "x-amz-date": amzDate, "x-amz-signature": sig,
	}}, nil
}

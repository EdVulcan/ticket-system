package utils

import (
	"crypto/md5"
	"encoding/hex"
	"math/rand"
	"sort"
	"strings"
	"time"
)

// GenerateRandomString generates a random string of length n
func GenerateRandomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[r.Intn(len(letters))]
	}
	return string(b)
}

// MD5 calculates MD5 hash of string
func MD5(str string) string {
	h := md5.New()
	h.Write([]byte(str))
	return hex.EncodeToString(h.Sum(nil))
}

// SignParams signs the request parameters for OTA
// Algo: MD5(k1=v1&k2=v2...&secret_key=SECRET)
func SignParams(params map[string]string, secretKey string) string {
	var keys []string
	for k := range params {
		if k != "sign" && params[k] != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString("&")
		}
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(params[k])
	}
	sb.WriteString("&secret_key=")
	sb.WriteString(secretKey)

	return MD5(sb.String())
}

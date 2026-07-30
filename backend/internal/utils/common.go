package utils

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"sort"
	"strings"
)

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func GenerateRandomString(n int) string {
	b := make([]rune, n)
	for i := range b {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			panic("secure random source unavailable: " + err.Error())
		}
		b[i] = letters[index.Int64()]
	}
	return string(b)
}

func MD5(str string) string {
	h := md5.New()
	h.Write([]byte(str))
	return hex.EncodeToString(h.Sum(nil))
}

// SignParams generates MD5 signature for OTA
func SignParams(params map[string]string, secretKey string) string {
	// 1. Sort keys
	var keys []string
	for k := range params {
		if k == "sign" || params[k] == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 2. Build string
	var buf strings.Builder
	for _, k := range keys {
		buf.WriteString(k)
		buf.WriteString("=")
		buf.WriteString(params[k])
		buf.WriteString("&")
	}
	buf.WriteString("key=" + secretKey)

	// 3. MD5
	return strings.ToUpper(MD5(buf.String()))
}

package utils

import (
	"crypto/rand"
)

var alphabet = []byte("0123456789abcdefghijklmnopqrstuvwxyz")

func UID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
package ids

import (
	"crypto/rand"
)

var alphabet = []byte("0123456789abcdefghijklmnopqrstuvwxyz")

func New() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(i)
		}
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
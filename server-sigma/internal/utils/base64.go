package utils

import "encoding/base64"

func B64(b []byte) string           { return base64.StdEncoding.EncodeToString(b) }
func FromB64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
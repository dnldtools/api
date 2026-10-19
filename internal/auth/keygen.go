package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const keyPrefix = "ra_"

func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func GenerateKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: read random bytes: %w", err)
	}
	return keyPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func ValidateCustomKey(key string) error {
	n := len(key)
	if n < 8 {
		return fmt.Errorf("auth: custom key must be at least 8 characters")
	}
	if n > 128 {
		return fmt.Errorf("auth: custom key must be at most 128 characters")
	}
	for _, c := range key {
		if c == ' ' || c < 0x20 || c == 0x7f {
			return fmt.Errorf("auth: custom key must not contain spaces or control characters")
		}
		ok := (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_' || c == '-' || c == '.'
		if !ok {
			return fmt.Errorf("auth: custom key contains invalid character %q (allowed: a-z A-Z 0-9 _ - .)", c)
		}
	}
	return nil
}

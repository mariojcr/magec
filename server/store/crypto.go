package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	encPrefix    = "enc:v1:"
	pbkdf2Iter   = 100_000
	pbkdf2KeyLen = 32
)

var pbkdf2Salt = []byte("magec-secrets-v1")

func deriveKey(password string) []byte {
	return pbkdf2.Key([]byte(password), pbkdf2Salt, pbkdf2Iter, pbkdf2KeyLen, sha256.New)
}

func encryptValue(plaintext, password string) (string, error) {
	key := deriveKey(password)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptValue(encoded, password string) (string, error) {
	if !strings.HasPrefix(encoded, encPrefix) {
		return encoded, nil
	}

	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, encPrefix))
	if err != nil {
		return "", fmt.Errorf("base64: %w", err)
	}

	key := deriveKey(password)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

func isEncrypted(value string) bool {
	return strings.HasPrefix(value, encPrefix)
}

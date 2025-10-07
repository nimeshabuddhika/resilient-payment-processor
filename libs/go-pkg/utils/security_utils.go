package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// EncryptAES encrypts the given plaintext using AES-256-GCM.
func EncryptAES(plaintext []byte, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

// DecodeString decodes a base64 encoded string to a byte array.
func DecodeString(value string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid AES key")
	}
	return key, nil
}

func EncodeString(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

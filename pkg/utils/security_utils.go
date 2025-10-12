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

// DecryptAES decrypts the given ciphertext using AES-256-GCM.
func DecryptAES(value string, key []byte) (string, error) {
	// Decode base64 input
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	// Ensure data has at least nonce size (12) + tag (16) minimal length
	if len(data) < 12+16 {
		return "", errors.New("ciphertext too short")
	}

	// Split nonce and ciphertext
	nonce := data[:12]
	ciphertext := data[12:]

	// Construct cipher and AEAD
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	// Decrypt
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// DecodeString decodes a base64 encoded string to a byte array.
func DecodeString(value string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid AES key")
	}
	return key, nil
}

// DecryptToFloat64 decrypts a string to a float64.
func DecryptToFloat64(value string, key []byte) (float64, error) {
	decryptStr, err := DecryptAES(value, key)
	if err != nil {
		return 0, err
	}
	return ToFloat64(decryptStr)
}

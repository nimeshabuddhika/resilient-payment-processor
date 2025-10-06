package pkg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"reflect"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/spf13/viper"
)

// GenerateUUID generates a new UUID.
func GenerateUUID() string {
	return uuid.New().String()
}

// IsEmpty checks if a string is empty.
func IsEmpty(s string) bool {
	return s == ""
}

func GetTraceID(c *gin.Context) (string, error) {
	traceID := c.GetString(TraceId)
	if IsEmpty(traceID) {
		return "", errors.New("trace id is empty")
	}
	return traceID, nil
}

// encryptAES encrypts data with AES-GCM; key from env (32 bytes for AES-256).
func encryptAES(plaintext []byte, key []byte) (string, error) {
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

// ParseStructEnv binds env vars to struct fields using a mapstructure tag
func ParseStructEnv(cfg interface{}) error {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("mapstructure")
		if err := viper.BindEnv(tag); err != nil {
			return err
		}
	}
	return viper.Unmarshal(cfg)
}

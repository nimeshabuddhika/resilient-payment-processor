package utils

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/spf13/viper"
)

// IsEmpty checks if a string is empty.
func IsEmpty(s string) bool {
	return s == ""
}

func GetTraceID(c *gin.Context) (string, error) {
	traceID := c.GetString(pkg.TraceId)
	if IsEmpty(traceID) {
		return "", errors.New("trace id is empty")
	}
	return traceID, nil
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

// ToFloat64 converts a numeric string to float64.
// It trims spaces and returns an error for invalid formats.
func ToFloat64(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

// BalanceToByte converts a balance to a byte array.
func BalanceToByte(bal float64) []byte {
	return []byte(fmt.Sprintf("%.2f", bal))
}

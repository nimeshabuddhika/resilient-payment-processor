package utils

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/spf13/viper"
	"go.uber.org/zap"
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

// Float64ToByte converts a float64 to a byte array.
func Float64ToByte(bal float64) []byte {
	return []byte(fmt.Sprintf("%.2f", bal))
}

// FormatConfigErrors formats validation errors into a human-readable message.
func FormatConfigErrors(logger *zap.Logger, err error, cfg interface{}) error {
	var varErr validator.ValidationErrors
	if errors.As(err, &varErr) {
		var parts []string
		for _, fe := range varErr {
			field := fe.Field()
			tag := fe.Tag()
			param := fe.Param()

			// Humanized message
			var msg string
			switch tag {
			case "required":
				msg = fmt.Sprintf("%s is required", field)
			case "min":
				msg = fmt.Sprintf("%s must be at least %s", field, param)
			case "max":
				msg = fmt.Sprintf("%s must be at most %s", field, param)
			default:
				if param != "" {
					msg = fmt.Sprintf("%s failed validation %s=%s", field, tag, param)
				} else {
					msg = fmt.Sprintf("%s failed validation %s", field, tag)
				}
			}

			logger.Error("invalid configuration",
				zap.String("field", field),
				zap.String("constraint", tag),
				zap.String("param", param),
			)
			parts = append(parts, msg)
		}
		return fmt.Errorf("invalid configuration: %s", strings.Join(parts, "; "))
	}
	return err
}

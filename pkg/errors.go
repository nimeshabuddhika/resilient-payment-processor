package pkg

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

var SqlErrForeignKeyViolation = errors.New("foreign key violation")
var SqlError = errors.New("sql error")

type ErrorCode string

const (
	ErrInvalidInput ErrorCode = "ERR_001"
	ERRServerError  ErrorCode = "ERR_002"
)

type ErrorResponse struct {
	Code    ErrorCode `json:"code"`    // internal error code
	Message string    `json:"message"` // user-friendly message
	Details string    `json:"details,omitempty"`
}

func (e ErrorResponse) Error() string {
	return e.Message
}

func HandleSQLError(traceId string, logger *zap.Logger, err error) error {
	var pgErr *pgconn.PgError
	errors.As(err, &pgErr)
	// Log concise warning without full stack
	logger.Error("sql error",
		zap.String(TraceId, traceId),
		zap.String("code", pgErr.Code),
		zap.String("message", pgErr.Message),
	)
	switch pgErr.Code {
	case "23503":
		return SqlErrForeignKeyViolation
	default:
		return SqlError
	}
}

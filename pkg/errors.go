package pkg

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

var (
	SqlErrForeignKeyViolation = errors.New("foreign key violation")
	SqlError                  = errors.New("sql error")
	ErrInsufficientBalance    = errors.New("insufficient balance") // New for business errors
)

type ErrorCode struct {
	Code    string
	Status  int
	Message string
}

var ErrInvalidInputCode = ErrorCode{Code: "ERR_001", Status: http.StatusBadRequest, Message: "invalid input"}
var ErrServerCode = ErrorCode{Code: "ERR_002", Status: http.StatusInternalServerError, Message: "internal server error"}
var ErrBusinessCode = ErrorCode{Code: "ERR_003", Status: http.StatusNotAcceptable, Message: "business error"}
var ErrRecordNotFoundCode = ErrorCode{Code: "ERR_004", Status: http.StatusNotFound, Message: "record not found"}

var ErrSQLCode = ErrorCode{Code: "ERR_001", Status: http.StatusNotAcceptable, Message: "sql error"}
var ErrSQLConflictsCode = ErrorCode{Code: "ERR_SQL_002", Status: http.StatusConflict, Message: "sql conflict"}
var ErrSQLDuplicateCode = ErrorCode{Code: "ERR_SQL_003", Status: http.StatusConflict, Message: "duplicate record"}
var ErrSQLInvalidInputCode = ErrorCode{Code: "ERR_SQL_004", Status: http.StatusNotAcceptable, Message: "invalid input"}

type AppError struct {
	Code    ErrorCode
	Message string
	Cause   error // Wrapped original error
}

func (e AppError) Error() string {
	return fmt.Sprintf("%s: %v", e.Message, e.Cause)
}

func (e AppError) Unwrap() error {
	return e.Cause
}

func NewAppError(code ErrorCode, msg string, cause error) error {
	return AppError{
		Code:    code,
		Message: msg,
		Cause:   cause,
	}
}

type ErrorResponse struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

func NewAppResponse(code ErrorCode) ErrorResponse {
	return ErrorResponse{
		Status:  code.Status,
		Code:    code.Code,
		Message: code.Message,
	}
}

func NewAppResponseMsg(code ErrorCode, msg string) ErrorResponse {
	return ErrorResponse{
		Status:  code.Status,
		Code:    code.Code,
		Message: msg,
	}
}

func NewAppResponseError(err error) ErrorResponse {
	var appErr AppError
	if errors.As(err, &appErr) {
		return ErrorResponse{
			Status:  appErr.Code.Status,
			Code:    appErr.Code.Code,
			Message: appErr.Message,
			Details: err.Error(),
		}
	}
	code := ErrServerCode
	return ErrorResponse{
		Status:  code.Status,
		Code:    code.Code,
		Message: code.Message,
		Details: err.Error(),
	}
}

// HandleSQLError handles SQL errors.
// It expands on HandleSQLError to cover common codes and better categories.
func HandleSQLError(traceId string, logger *zap.Logger, err error) error {
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) {
		// no records found
		logger.Error("no records found", zap.String(TraceId, traceId))
		return NewAppError(ErrRecordNotFoundCode, "no records found", err)
	} else if !errors.As(err, &pgErr) {
		logger.Error("unknown error", zap.String(TraceId, traceId), zap.Error(err))
		return NewAppError(ErrSQLCode, "unknown error", err)
	}

	// Log rich pg error context
	logger.Error("sql error",
		zap.String(TraceId, traceId),
		zap.String("code", pgErr.Code),
		zap.String("message", pgErr.Message),
		zap.String("detail", pgErr.Detail),
		zap.String("schema", pgErr.SchemaName),
		zap.String("table", pgErr.TableName),
		zap.String("column", pgErr.ColumnName),
		zap.String("constraint", pgErr.ConstraintName),
	)

	switch pgErr.Code {
	// Integrity violations -> conflict or bad request
	case "23505": // unique_violation
		return NewAppError(ErrSQLDuplicateCode, "duplicate value violates unique constraint", SqlError)
	case "23503": // foreign_key_violation
		return NewAppError(ErrSQLConflictsCode, "foreign key violation", SqlErrForeignKeyViolation)

	// Data exceptions -> bad request
	case "22P02": // invalid_text_representation (e.g., bad UUID)
		return NewAppError(ErrSQLInvalidInputCode, "invalid input syntax", SqlError)
	case "22001": // string_data_right_truncation
		return NewAppError(ErrSQLInvalidInputCode, "value too long for column", SqlError)
	case "22003": // numeric_value_out_of_range
		return NewAppError(ErrSQLInvalidInputCode, "numeric value out of range", SqlError)

	default:
		return NewAppError(ErrSQLCode, "sql error", SqlError)
	}
}

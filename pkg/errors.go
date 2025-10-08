package pkg

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

var ExposeErrorDetails = false

func init() {
	if gin.DebugMode == gin.Mode() || gin.TestMode == gin.Mode() {
		ExposeErrorDetails = true
	}
}

// Reusable errors
var (
	SqlErrForeignKeyViolation = errors.New("foreign key violation")
	SqlError                  = errors.New("sql error")
	ErrInsufficientBalance    = errors.New("insufficient balance")
)

// ErrorCode defines a standardized error code
type ErrorCode struct {
	Code    string
	Status  int
	Message string // default message
}

var (
	// Generic app
	ErrInvalidInputCode   = ErrorCode{Code: "APP_INVALID_INPUT", Status: http.StatusBadRequest, Message: "invalid input"}
	ErrServerCode         = ErrorCode{Code: "APP_INTERNAL", Status: http.StatusInternalServerError, Message: "internal server error"}
	ErrRecordNotFoundCode = ErrorCode{Code: "APP_NOT_FOUND", Status: http.StatusNotFound, Message: "record not found"}

	// Business/domain rules
	ErrBusinessRuleCode        = ErrorCode{Code: "BUSINESS_RULE_VIOLATION", Status: http.StatusUnprocessableEntity, Message: "business rule violated"}
	ErrInsufficientFundsCode   = ErrorCode{Code: "BUSINESS_INSUFFICIENT_FUNDS", Status: http.StatusUnprocessableEntity, Message: "insufficient balance"}
	ErrIdempotencyConflictCode = ErrorCode{Code: "BUSINESS_IDEMPOTENCY_CONFLICT", Status: http.StatusConflict, Message: "idempotency conflict"}

	// SQL layer
	ErrSQLUnknownCode   = ErrorCode{Code: "SQL_UNKNOWN", Status: http.StatusInternalServerError, Message: "sql error"}
	ErrSQLConflictCode  = ErrorCode{Code: "SQL_CONFLICT", Status: http.StatusConflict, Message: "sql conflict"}
	ErrSQLDuplicateCode = ErrorCode{Code: "SQL_DUPLICATE", Status: http.StatusConflict, Message: "duplicate record"}
	ErrSQLInvalidInput  = ErrorCode{Code: "SQL_INVALID_INPUT", Status: http.StatusBadRequest, Message: "invalid input"}
)

type AppError struct {
	Code    ErrorCode
	Message string // public-facing message
	Cause   error  // internal cause (wrapped)
}

func (e AppError) Error() string {
	if e.Cause == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Cause)
}
func (e AppError) Unwrap() error { return e.Cause }

func NewAppError(code ErrorCode, msg string, cause error) error {
	return AppError{Code: code, Message: msg, Cause: cause}
}

// ErrorResponse defines the standardized error response format
type ErrorResponse struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// ToErrorResponse converts an error into an ErrorResponse, logging details and optionally exposing error messages.
// If the error is not an AppError, it is converted to a generic 500 error.
func ToErrorResponse(logger *zap.Logger, traceID string, err error) ErrorResponse {
	var appErr AppError
	if errors.As(err, &appErr) {
		resp := ErrorResponse{
			Status:  appErr.Code.Status,
			Code:    appErr.Code.Code,
			Message: appErr.Message,
		}
		logger.Error("application error", zap.String(TraceId, traceID), zap.Error(err))
		if ExposeErrorDetails {
			resp.Details = err.Error()
		}
		return resp
	}
	// Unknown error : 500
	resp := ErrorResponse{
		Status:  ErrServerCode.Status,
		Code:    ErrServerCode.Code,
		Message: ErrServerCode.Message,
	}
	logger.Error("application error", zap.String(TraceId, traceID), zap.Error(err))
	if ExposeErrorDetails {
		resp.Details = err.Error()
	}
	return resp
}

// HandleSQLError maps pg errors -> AppError with proper codes/status
func HandleSQLError(traceId string, logger *zap.Logger, err error) error {
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) {
		logger.Warn("sql error : no records found", zap.String(TraceId, traceId))
		return NewAppError(ErrRecordNotFoundCode, "no records found", err)
	}
	if !errors.As(err, &pgErr) {
		logger.Error("sql error : unknown", zap.String(TraceId, traceId), zap.Error(err))
		return NewAppError(ErrSQLUnknownCode, "sql error", err)
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
	case "23505": // unique_violation
		return NewAppError(ErrSQLDuplicateCode, "duplicate value violates unique constraint", SqlError)
	case "23503": // foreign_key_violation
		return NewAppError(ErrSQLConflictCode, "foreign key violation", SqlErrForeignKeyViolation)
	case "22P02": // invalid_text_representation Ex: bad UUID
		return NewAppError(ErrSQLInvalidInput, "invalid input syntax", SqlError)
	case "22001": // string_data_right_truncation
		return NewAppError(ErrSQLInvalidInput, "value too long for column", SqlError)
	case "22003": // numeric_value_out_of_range
		return NewAppError(ErrSQLInvalidInput, "numeric value out of range", SqlError)
	default:
		return NewAppError(ErrSQLUnknownCode, "sql error", SqlError)
	}
}

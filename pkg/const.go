package pkg

const (
	HeaderTraceId   string = "X-Trace-Id"
	HeaderRequestId string = "X-Request-Id"
)

const (
	TraceId        string = "trace_id"
	RequestId      string = "request_id"
	UserId         string = "user_id"
	IdempotencyKey string = "idempotency_key"
)

type OrderStatus string

const (
	OrderStatusPending  OrderStatus = "pending"
	OrderStatusSuccess  OrderStatus = "success"
	OrderStatusRetrying OrderStatus = "retrying"
	OrderStatusFailed   OrderStatus = "failed"
)

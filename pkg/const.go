package pkg

const (
	HeaderTraceId   string = "X-Trace-Id"
	HeaderRequestId string = "X-Request-Id"
)

const (
	TraceId        string = "trace_id"
	RequestId      string = "request_id"
	IdempotencyKey string = "idempotency_key"
)

type OrderStatus string

const (
	OrderStatusPending OrderStatus = "pending"
	OrderStatusSuccess OrderStatus = "success"
	OrderStatusRetying OrderStatus = "retrying"
	OrderStatusFailed  OrderStatus = "failed"
)

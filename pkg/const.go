package pkg

const (
	HeaderTraceId   string = "X-Trace-Id"
	HeaderRequestId string = "X-Request-Id"
)

const (
	TraceId   string = "trace_id"
	RequestId string = "request_id"
)

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusProcessed OrderStatus = "processed"
	OrderStatusRetrying  OrderStatus = "retrying"
	OrderStatusFailed    OrderStatus = "failed"
)

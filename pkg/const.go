package pkg

const (
	HeaderTraceId   string = "X-Trace-Id"
	HeaderRequestId string = "X-Request-Id"
	HeaderUserId    string = "user_id"
)

const (
	TraceId        string = "trace_id"
	RequestId      string = "request_id"
	IdempotencyKey string = "idempotency_key"
	Account_Id     string = "account_id"
)

type OrderStatus string

const (
	OrderStatusPending  OrderStatus = "pending"
	OrderStatusSuccess  OrderStatus = "success"
	OrderStatusRetrying OrderStatus = "retrying"
	OrderStatusFailed   OrderStatus = "failed"
)

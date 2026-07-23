package delivery

// DeliveryStatus tracks the lifecycle of an outbox delivery ATTEMPT.
type DeliveryStatus string

const (
	DeliveryPending     DeliveryStatus = "PENDING"
	DeliveryLeased      DeliveryStatus = "LEASED"
	DeliveryRunning     DeliveryStatus = "RUNNING"
	DeliveryRetryWait   DeliveryStatus = "RETRY_WAIT"
	DeliverySucceeded   DeliveryStatus = "SUCCEEDED"
	DeliveryFailed      DeliveryStatus = "FAILED"
	DeliveryBlockedAuth DeliveryStatus = "BLOCKED_AUTH"
	DeliveryCancelled   DeliveryStatus = "CANCELLED"
)

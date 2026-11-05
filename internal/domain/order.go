package domain

// OrderStatus is the lifecycle state of an online order (§12.1).
type OrderStatus string

const (
	OrderPendingPayment    OrderStatus = "PENDING_PAYMENT"
	OrderPaid              OrderStatus = "PAID"
	OrderFulfilled         OrderStatus = "FULFILLED"
	OrderCancelled         OrderStatus = "CANCELLED"
	OrderExpired           OrderStatus = "EXPIRED"
	OrderNeedsReallocation OrderStatus = "NEEDS_REALLOCATION"
)

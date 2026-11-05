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

// Channel is the sales channel of an online order. Store stock is only for D2C (§12.3).
type Channel string

const (
	ChannelD2C         Channel = "D2C"
	ChannelMarketplace Channel = "MARKETPLACE"
)

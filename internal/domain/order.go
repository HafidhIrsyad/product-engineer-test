package domain

type OrderStatus string

const (
	OrderPendingPayment    OrderStatus = "PENDING_PAYMENT"
	OrderPaid              OrderStatus = "PAID"
	OrderFulfilled         OrderStatus = "FULFILLED"
	OrderCancelled         OrderStatus = "CANCELLED"
	OrderExpired           OrderStatus = "EXPIRED"
	OrderNeedsReallocation OrderStatus = "NEEDS_REALLOCATION"
)

type Channel string

const (
	ChannelD2C         Channel = "D2C"
	ChannelMarketplace Channel = "MARKETPLACE"
)

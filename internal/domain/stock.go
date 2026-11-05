package domain

type MovementReason string

const (
	MovementReceive           MovementReason = "RECEIVE"
	MovementShipOut           MovementReason = "SHIP_OUT"
	MovementReserve           MovementReason = "RESERVE"
	MovementRelease           MovementReason = "RELEASE"
	MovementSaleOnline        MovementReason = "SALE_ONLINE"
	MovementSalePOS           MovementReason = "SALE_POS"
	MovementSellOutInferred   MovementReason = "SELL_OUT_INFERRED"
	MovementQuarantine        MovementReason = "QUARANTINE"
	MovementReleaseQuarantine MovementReason = "RELEASE_QUARANTINE"
)

type LocationType string

const (
	LocationFactory          LocationType = "FACTORY"
	LocationCentralWarehouse LocationType = "CENTRAL_WAREHOUSE"
	LocationHub              LocationType = "HUB"
	LocationOwnStore         LocationType = "OWN_STORE"
	LocationPartner          LocationType = "PARTNER"
)

// RunoutWarningDays is the lead time (7) plus the safety margin (3): below this many days of
// stock, a location needs a refill now.
const RunoutWarningDays = 10

// SalesWindowDays is how far back average daily sales look.
const SalesWindowDays = 14

// DaysOfInventory estimates how long the good stock lasts at the recent sales rate.
// It is nil when nothing was sold in the window: there is no rate to divide by.
func DaysOfInventory(onHand, quarantine int32, soldInWindow int64) *float64 {
	if soldInWindow <= 0 {
		return nil
	}
	perDay := float64(soldInWindow) / SalesWindowDays
	days := float64(onHand-quarantine) / perDay
	return &days
}

// RunoutWarning is true when the stock will not last until a refill arrives.
func RunoutWarning(days *float64) bool {
	return days != nil && *days < RunoutWarningDays
}

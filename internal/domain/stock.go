package domain

// MovementReason explains why a stock counter changed (ledger, §11).
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

// LocationType is where stock can be (§7).
type LocationType string

const (
	LocationFactory          LocationType = "FACTORY"
	LocationCentralWarehouse LocationType = "CENTRAL_WAREHOUSE"
	LocationHub              LocationType = "HUB"
	LocationOwnStore         LocationType = "OWN_STORE"
	LocationPartner          LocationType = "PARTNER"
)

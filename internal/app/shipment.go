package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

type ShipInput struct {
	ContainerCode  string `json:"container_code"`
	ToLocationCode string `json:"to_location_code"`
}

type ReceiveInput struct {
	ContainerCode string `json:"container_code"`
	LocationCode  string `json:"location_code"`
}

type ShipmentView struct {
	Container string              `json:"container_code"`
	Status    domain.SerialStatus `json:"status"`
	Location  string              `json:"location_code"`
	Units     int                 `json:"units"`
}

// container is a pallet, carton or single unit with everything packed under it, all locked.
type container struct {
	ids        []int64
	status     domain.SerialStatus
	locationID int64
	units      []stockLine // units per SKU
	unitCount  int
}

// loadContainer locks the container and everything under it. All of it must be in one status and one
// location: a half-moved or half-sold pallet cannot travel as a whole.
func loadContainer(ctx context.Context, q *store.Queries, code string) (container, error) {
	rows, err := q.ListContainerTree(ctx, code)
	if err != nil {
		return container{}, err
	}
	if len(rows) == 0 {
		return container{}, fmt.Errorf("%w: %s", domain.ErrSerialNotFound, code)
	}
	c := container{status: rows[0].Status, locationID: rows[0].LocationID}
	perSKU := map[int64]int32{}
	for _, r := range rows {
		if r.Status != c.status || r.LocationID != c.locationID {
			return container{}, fmt.Errorf("%w: %s", domain.ErrMixedContainer, code)
		}
		c.ids = append(c.ids, r.ID)
		if r.Level == domain.LevelUnit {
			perSKU[r.SkuID]++
			c.unitCount++
		}
	}
	for sku, qty := range perSKU {
		c.units = append(c.units, stockLine{skuID: sku, qty: qty})
	}
	slices.SortFunc(c.units, func(a, b stockLine) int { return cmp.Compare(a.skuID, b.skuID) })
	return c, nil
}

// Ship sends a container to another location. The units become IN_TRANSIT, and when the destination
// is a partner they are tied to that distributor: this is where the allocation is recorded.
func (s *Service) Ship(ctx context.Context, in ShipInput) (ShipmentView, error) {
	var view ShipmentView
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		c, err := loadContainer(ctx, q, in.ContainerCode)
		if err != nil {
			return err
		}
		dest, err := q.GetLocationByCode(ctx, in.ToLocationCode)
		if store.IsNotFound(err) {
			return fmt.Errorf("%w: %s", domain.ErrUnknownLocation, in.ToLocationCode)
		}
		if err != nil {
			return err
		}
		if dest.Type == domain.LocationFactory || dest.ID == c.locationID {
			return fmt.Errorf("%w: the destination must be a different location, and not a factory", domain.ErrInvalidInput)
		}
		if err := moveSerials(ctx, q, c.ids, c.status, domain.SerialInTransit, dest.ID, dest.PartnerID, "SHIPMENT", in.ContainerCode); err != nil {
			return err
		}
		if err := shipOut(ctx, q, in.ContainerCode, c.locationID, c.units); err != nil {
			return err
		}
		view = ShipmentView{Container: in.ContainerCode, Status: domain.SerialInTransit, Location: dest.Code, Units: c.unitCount}
		return nil
	})
	return view, err
}

// Receive books a container in. Fresh from the factory it enters the central warehouse (IN_WAREHOUSE);
// after a shipment it arrives at its destination (IN_STOCK). Either way the units become stock.
func (s *Service) Receive(ctx context.Context, in ReceiveInput) (ShipmentView, error) {
	var view ShipmentView
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		c, err := loadContainer(ctx, q, in.ContainerCode)
		if err != nil {
			return err
		}
		dest, err := q.GetLocationByCode(ctx, in.LocationCode)
		if store.IsNotFound(err) {
			return fmt.Errorf("%w: %s", domain.ErrUnknownLocation, in.LocationCode)
		}
		if err != nil {
			return err
		}
		var to domain.SerialStatus
		switch c.status {
		case domain.SerialProduced:
			to = domain.SerialInWarehouse
			if dest.Type != domain.LocationCentralWarehouse {
				return fmt.Errorf("%w: new production is received at a central warehouse", domain.ErrInvalidInput)
			}
		case domain.SerialInTransit:
			to = domain.SerialInStock
			if c.locationID != dest.ID {
				return fmt.Errorf("%w: %s", domain.ErrWrongLocation, in.LocationCode)
			}
		default:
			return fmt.Errorf("%w: a %s container cannot be received", domain.ErrInvalidTransition, c.status)
		}
		if err := moveSerials(ctx, q, c.ids, c.status, to, dest.ID, nil, "SHIPMENT", in.ContainerCode); err != nil {
			return err
		}
		if err := receiveStock(ctx, q, in.ContainerCode, dest.ID, c.units); err != nil {
			return err
		}
		view = ShipmentView{Container: in.ContainerCode, Status: to, Location: dest.Code, Units: c.unitCount}
		return nil
	})
	return view, err
}

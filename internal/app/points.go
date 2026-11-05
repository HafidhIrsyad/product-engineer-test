package app

import (
	"context"
	"strconv"
	"time"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

type ClaimInput struct {
	MemberID   int64  `json:"member_id"`
	SerialCode string `json:"serial_code"`
	InnerCode  string `json:"inner_code"`
}

type ClaimView struct {
	ID          int64     `json:"id"`
	SerialCode  string    `json:"serial_code"`
	Points      int32     `json:"points"`
	Status      string    `json:"status"`
	AvailableAt time.Time `json:"available_at"`
}

// ClaimPoints rewards a member for a unit bought at a partner. The proof is the inner code under
// the seal, which a cashier or middleman cannot read without opening the pack. Scanning the outer
// QR never creates points. The claim also tells us the unit was sold, so its stock is counted out.
func (s *Service) ClaimPoints(ctx context.Context, in ClaimInput) (ClaimView, error) {
	var view ClaimView
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		row, err := q.GetSerialForClaim(ctx, in.SerialCode)
		// Unknown unit, wrong code and wrong kind of unit all look the same from outside.
		if store.IsNotFound(err) || (err == nil && (row.Level != domain.LevelUnit || !domain.InnerCodeMatches(row.InnerCodeHash, in.InnerCode))) {
			return domain.ErrProofRejected
		}
		if err != nil {
			return err
		}
		if row.Status == domain.SerialVerified {
			return domain.ErrAlreadyClaimed
		}
		if row.Status != domain.SerialInStock || row.LocationType != domain.LocationPartner {
			return domain.ErrProofRejected
		}

		if _, err := transitionSerial(ctx, q, row.ID, row.Status, domain.SerialVerified, &row.LocationID, "CLAIM", strconv.FormatInt(in.MemberID, 10)); err != nil {
			return err
		}
		if err := sellOutInferred(ctx, q, row.ID, row.LocationID, row.SkuID); err != nil {
			return err
		}
		recent, err := q.CountRecentClaims(ctx, in.MemberID)
		if err != nil {
			return err
		}
		status := "PENDING"
		if recent >= domain.MaxClaimsPerDay {
			status = "HELD" // too fast for a person: a human reviews it
		}
		claim, err := q.InsertPointClaim(ctx, store.InsertPointClaimParams{
			SerialID: row.ID, MemberID: in.MemberID, Points: domain.PointsPerUnit, Status: status, WaitHours: domain.ClaimReviewHours,
		})
		if err != nil {
			return err
		}
		view = ClaimView{ID: claim.ID, SerialCode: in.SerialCode, Points: domain.PointsPerUnit, Status: status, AvailableAt: claim.AvailableAt}
		return nil
	})
	return view, err
}

type MemberPoints struct {
	MemberID        int64       `json:"member_id"`
	AvailablePoints int32       `json:"available_points"`
	PendingPoints   int32       `json:"pending_points"`
	HeldPoints      int32       `json:"held_points"`
	Claims          []ClaimView `json:"claims"`
}

// Points shows what a member has claimed and what can be redeemed.
func (s *Service) Points(ctx context.Context, memberID int64) (MemberPoints, error) {
	rows, err := s.store.ListMemberClaims(ctx, memberID)
	if err != nil {
		return MemberPoints{}, err
	}
	out := MemberPoints{MemberID: memberID, Claims: make([]ClaimView, len(rows))}
	for i, r := range rows {
		out.Claims[i] = ClaimView{ID: r.ID, SerialCode: r.SerialCode, Points: r.Points, Status: r.Status, AvailableAt: r.AvailableAt}
		switch r.Status {
		case "AVAILABLE":
			out.AvailablePoints += r.Points
		case "HELD":
			out.HeldPoints += r.Points
		default:
			out.PendingPoints += r.Points
		}
	}
	return out, nil
}

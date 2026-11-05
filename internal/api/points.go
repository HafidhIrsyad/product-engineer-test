package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

func (h *handler) claimPoints(w http.ResponseWriter, r *http.Request) {
	var in app.ClaimInput
	if err := decodeBody(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	code, err := domain.ParseSerialCode(in.SerialCode)
	if err != nil {
		writeError(w, r, err)
		return
	}
	in.SerialCode = string(code)
	if in.MemberID <= 0 || in.InnerCode == "" || len(in.InnerCode) > maxDeviceIDLen {
		writeError(w, r, fmt.Errorf("%w: member_id and inner_code are required", domain.ErrInvalidInput))
		return
	}
	view, err := h.svc.ClaimPoints(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (h *handler) memberPoints(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, r, fmt.Errorf("%w: member id must be a positive number", domain.ErrInvalidInput))
		return
	}
	points, err := h.svc.Points(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, points)
}

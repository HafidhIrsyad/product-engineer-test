package api

import (
	"fmt"
	"net/http"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

func (h *handler) verify(w http.ResponseWriter, r *http.Request) {
	in, err := decodeVerify(w, r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.svc.Verify(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view) // NOT_FOUND is a normal answer, not an error
}

func decodeVerify(w http.ResponseWriter, r *http.Request) (app.VerifyInput, error) {
	var in app.VerifyInput
	if err := decodeBody(w, r, &in); err != nil {
		return in, err
	}
	code, err := domain.ParseSerialCode(string(in.Code))
	if err != nil {
		return in, err
	}
	in.Code = code
	if in.DeviceID == "" || len(in.DeviceID) > maxDeviceIDLen {
		return in, fmt.Errorf("%w: device_id is required (at most %d characters)", domain.ErrInvalidInput, maxDeviceIDLen)
	}
	if (in.Lat == nil) != (in.Lng == nil) {
		return in, fmt.Errorf("%w: lat and lng must be given together", domain.ErrInvalidInput)
	}
	if in.Lat != nil && (*in.Lat < -90 || *in.Lat > 90 || *in.Lng < -180 || *in.Lng > 180) {
		return in, fmt.Errorf("%w: lat must be within ±90 and lng within ±180", domain.ErrInvalidInput)
	}
	return in, nil
}

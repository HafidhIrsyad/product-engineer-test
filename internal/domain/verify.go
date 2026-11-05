package domain

import (
	"math"
	"slices"
	"time"
)

type Verdict string

const (
	VerdictGenuine            Verdict = "GENUINE"
	VerdictGenuineWithWarning Verdict = "GENUINE_WITH_WARNING"
	VerdictNotFound           Verdict = "NOT_FOUND"
)

type AnomalyType string

const (
	AnomalyImpossibleTravel  AnomalyType = "IMPOSSIBLE_TRAVEL"   // R1
	AnomalyScanBeforeRelease AnomalyType = "SCAN_BEFORE_RELEASE" // R2
	AnomalyOutOfRegion       AnomalyType = "OUT_OF_REGION"       // R3
	AnomalyPosSerialMismatch AnomalyType = "POS_SERIAL_MISMATCH"
	AnomalyOverSold          AnomalyType = "OVERSOLD"
)

const (
	// An impossible-travel scan needs both: farther than this AND faster than a commercial flight.
	ImpossibleTravelMinKm = 100.0
	MaxPlausibleSpeedKmh  = 800.0
	// Distinct devices that scanned a code before the consumer is warned.
	WarnDistinctDevices = 3
)

const earthRadiusKm = 6371.0

// Point is a WGS84 coordinate.
type Point struct {
	Lat, Lng float64
}

// HaversineKm returns the great-circle distance between two points, in kilometres.
func HaversineKm(a, b Point) float64 {
	dLat := toRad(b.Lat - a.Lat)
	dLng := toRad(b.Lng - a.Lng)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(a.Lat))*math.Cos(toRad(b.Lat))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return earthRadiusKm * 2 * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
}

func toRad(deg float64) float64 { return deg * math.Pi / 180 }

type Region struct {
	Center   Point
	RadiusKm float64
}

type PreviousScan struct {
	At       time.Time
	Location *Point
}

type ScanInput struct {
	Status          SerialStatus
	At              time.Time
	Location        *Point
	Region          *Region       // set only when the serial is allocated to a distributor
	Previous        *PreviousScan // nil when no other device has scanned this code
	DistinctDevices int           // devices that scanned this code, including the current one
}

// Result is the outcome of evaluating one scan.
type Result struct {
	Verdict   Verdict
	Anomalies []AnomalyType
}

func Evaluate(in ScanInput) Result {
	var anomalies []AnomalyType
	if impossibleTravel(in) {
		anomalies = append(anomalies, AnomalyImpossibleTravel)
	}
	if scannedBeforeRelease(in.Status) {
		anomalies = append(anomalies, AnomalyScanBeforeRelease)
	}
	if outOfRegion(in) {
		anomalies = append(anomalies, AnomalyOutOfRegion)
	}
	return Result{
		Verdict:   DecideVerdict(true, anomalies, in.DistinctDevices),
		Anomalies: anomalies,
	}
}

func DecideVerdict(found bool, anomalies []AnomalyType, distinctDevices int) Verdict {
	switch {
	case !found:
		return VerdictNotFound
	case len(anomalies) > 0 || distinctDevices >= WarnDistinctDevices:
		return VerdictGenuineWithWarning
	default:
		return VerdictGenuine
	}
}

// R1: the code was scanned by another device far away, too recently to have travelled there.
func impossibleTravel(in ScanInput) bool {
	if in.Previous == nil || in.Previous.Location == nil || in.Location == nil {
		return false
	}
	km := HaversineKm(*in.Previous.Location, *in.Location)
	if km <= ImpossibleTravelMinKm {
		return false
	}
	hours := in.At.Sub(in.Previous.At).Hours()
	if hours <= 0 {
		return true // same instant (or clocks reversed) at two far places
	}
	return km/hours > MaxPlausibleSpeedKmh
}

// R2: the unit has not left the factory or warehouse, so no consumer should hold it.
func scannedBeforeRelease(status SerialStatus) bool {
	return slices.Contains(
		[]SerialStatus{SerialProduced, SerialInWarehouse, SerialInTransit, SerialQuarantine},
		status,
	)
}

// R3: the unit is allocated to a distributor but scanned outside its region.
func outOfRegion(in ScanInput) bool {
	if in.Region == nil || in.Location == nil {
		return false
	}
	return HaversineKm(in.Region.Center, *in.Location) > in.Region.RadiusKm
}

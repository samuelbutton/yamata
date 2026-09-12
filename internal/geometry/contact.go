// Package geometry defines integer interval contact shared by simulation and scoring.
package geometry

// Contact tests swept relative rear-edge displacement, including touching edges.
// Inputs use the bounded integer units from the version-one contract.
func Contact(start, end, vehicleLength, obstacleLength int64) bool {
	return min(start, end) <= vehicleLength && max(start, end) >= -obstacleLength
}

package simulator

const brakingMMS2 int64 = 4000
const safetyGapMM int64 = 2000

type controller struct {
	kind            Controller
	tickMS          int64
	vehicleLengthMM int64
	braking         bool
}

func (c *controller) acceleration(state Record) int64 {
	if !c.braking {
		stoppingDistance := ceilDiv(state.SpeedMMS*state.SpeedMMS, 2*brakingMMS2)
		threshold := stoppingDistance + ceilDiv(state.SpeedMMS*c.tickMS, 1000) + safetyGapMM
		if c.kind == Candidate {
			threshold = stoppingDistance / 4
		}
		for _, o := range state.Obstacles {
			if o.PositionMM+o.LengthMM < state.PositionMM {
				continue
			}
			gap := o.PositionMM - state.PositionMM - c.vehicleLengthMM
			if gap <= threshold {
				c.braking = true
				break
			}
		}
	}
	// Once braking starts, both policies hold the brake until the vehicle stops.
	if c.braking {
		return -brakingMMS2
	}
	return 0
}

func ceilDiv(numerator, denominator int64) int64 { return (numerator + denominator - 1) / denominator }

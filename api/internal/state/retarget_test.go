package state

import (
	"math"
	"testing"
)

// The estimate must be stable from the very first blocks of an epoch:
// three blocks on target pace should read the same ~0% as a thousand.
// The old wall-clock measurement made early readings swing wildly
// because the time since the last block, 600s on average, was counted
// into the elapsed span without a matching interval to divide it by.
func TestEstimateRetargetPercent_StableEarlyInEpoch(t *testing.T) {
	// Exactly on-pace: each interval takes the consensus target.
	onPace := float64(retargetInterval*targetBlockSeconds) / float64(retargetInterval-1)
	for _, inEpoch := range []int64{1, 3, 10, 100, 1008, 2015} {
		got := estimateRetargetPercent(inEpoch, float64(inEpoch)*onPace)
		if math.Abs(got) > 0.01 {
			t.Errorf("inEpoch=%d on-pace: got %+.4f%%, want ~0%%", inEpoch, got)
		}
	}
}

func TestEstimateRetargetPercent_Pace(t *testing.T) {
	// On-target pace per inter-block interval, including the consensus
	// 2016/2015 off-by-one. Expressing the cases against it keeps the
	// expected percentages exact instead of approximate.
	onPace := float64(retargetInterval*targetBlockSeconds) / float64(retargetInterval-1)
	tests := []struct {
		name    string
		inEpoch int64
		avgSecs float64
		want    float64
	}{
		// Hashrate 1.1x target -> difficulty rises 10% to match it.
		{"fast blocks raise difficulty", 500, onPace / 1.1, 10},
		// Blocks taking 1.1x as long -> difficulty falls to 1/1.1.
		{"slow blocks lower difficulty", 500, onPace * 1.1, -9.09},
		// Consensus clamps: 10x fast is capped at +300%.
		{"clamped at +300", 100, 60, 300},
		// 10x slow is capped at -75%.
		{"clamped at -75", 100, 6000, -75},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := estimateRetargetPercent(tc.inEpoch, float64(tc.inEpoch)*tc.avgSecs)
			if math.Abs(got-tc.want) > 0.1 {
				t.Errorf("got %+.2f%%, want %+.2f%%", got, tc.want)
			}
		})
	}
}

// Guards against a divide-by-zero or a nonsense reading at the exact
// epoch boundary, where no interval has elapsed yet.
func TestEstimateRetargetPercent_Degenerate(t *testing.T) {
	for _, tc := range []struct {
		inEpoch int64
		elapsed float64
	}{{0, 0}, {0, 1000}, {5, 0}, {-1, 600}, {5, -600}} {
		if got := estimateRetargetPercent(tc.inEpoch, tc.elapsed); got != 0 {
			t.Errorf("inEpoch=%d elapsed=%v: got %v, want 0", tc.inEpoch, tc.elapsed, got)
		}
	}
}

// The bias the fix removes: measuring to wall-clock adds the partial
// interval since the last block to the elapsed span. This reproduces
// that arithmetic to show how large the resulting error was early in an
// epoch versus at the midpoint.
func TestEstimateRetargetPercent_WallClockBiasWasWorstEarly(t *testing.T) {
	onPace := float64(retargetInterval*targetBlockSeconds) / float64(retargetInterval-1)
	const sinceLastBlock = 600.0 // the long-run average wait

	early := estimateRetargetPercent(3, 3*onPace+sinceLastBlock)
	mid := estimateRetargetPercent(1008, 1008*onPace+sinceLastBlock)

	if math.Abs(early) < 15 {
		t.Errorf("early-epoch wall-clock error = %+.2f%%, expected a large skew", early)
	}
	if math.Abs(mid) > 0.5 {
		t.Errorf("mid-epoch wall-clock error = %+.2f%%, expected it to have washed out", mid)
	}
	// And with block timestamps, that same on-pace epoch reads ~0 at
	// both points, which is the actual fix.
	if got := estimateRetargetPercent(3, 3*onPace); math.Abs(got) > 0.01 {
		t.Errorf("block-timestamp measurement at 3 blocks = %+.4f%%, want ~0%%", got)
	}
}

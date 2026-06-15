package engine

import (
	t "dcbapp/internal/dcbtypes"
)

// RegionCapacities returns each region's installed nameplate compute (CU) summed
// across accelerator types — a display helper for the UI. The authoritative
// operable figure (with the reciprocity feedback and events applied) is computed
// inside Step.
func RegionCapacities(s *t.State) [t.NREGION]int64 {
	var out [t.NREGION]int64
	for r := 0; r < t.NREGION; r++ {
		var cu int64
		for k := 0; k < t.NACCEL; k++ {
			cu += s.Servers[k][r] * Accel[k].CUPerUnit
		}
		out[r] = cu
	}
	return out
}

// RegionServers returns each region's total installed unit count across types.
func RegionServers(s *t.State) [t.NREGION]int64 {
	var out [t.NREGION]int64
	for r := 0; r < t.NREGION; r++ {
		var n int64
		for k := 0; k < t.NACCEL; k++ {
			n += s.Servers[k][r]
		}
		out[r] = n
	}
	return out
}

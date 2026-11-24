package nodecli

// Preset is one of MeshCore's regional radio presets.
type Preset struct {
	// Title is the preset's name as the API gives it ("EU/UK (Narrow)").
	Title string
	// Radio is its radio setting.
	Radio Radio
	// PathHashBytes is the preset's path_hash_size, sent as path.hash.mode size−1 on v1.14.0+ (as config.meshcore.io
	// does, src/gui.js:280-302), or 0 when the preset doesn't give one.
	PathHashBytes int
}

// Presets are the 27 regional presets served by the MeshCore App API (https://api.meshcore.nz/api/v1/config), as
// copied on 19 Nov 2026 and recorded in docs/meshcore-flashing.md 7.8 (fetched 24 Nov 2026), in the API's order.
// They live on that server, not in MeshCore's repository, so MeshCore's MIT licence doesn't cover them and the list
// has no stated licence (UNVERIFIED terms); the owner chose a built-in copy knowing that, so setup works with no
// network (SPEC.md 7, THIRD_PARTY_NOTICES 6). Values are strings in the JSON; CR 5 means 4/5.
var Presets = []Preset{
	{"Australia", Radio{915.800, 250, 10, 5}, 2},
	{"Australia (Narrow)", Radio{916.575, 62.5, 7, 7}, 2},
	{"Australia (Mid)", Radio{915.075, 125, 9, 5}, 2},
	{"Australia: SA, WA", Radio{923.125, 62.5, 8, 8}, 2},
	{"Australia: QLD", Radio{923.125, 62.5, 8, 5}, 2},
	{"Brazil", Radio{923.125, 62.5, 8, 8}, 0},
	{"Canada", Radio{910.525, 62.5, 7, 5}, 3},
	{"Costa Rica", Radio{910.525, 125, 11, 5}, 0},
	{"EU/UK (Narrow)", Radio{869.618, 62.5, 8, 8}, 0},
	{"EU/UK (Deprecated)", Radio{869.525, 250, 11, 5}, 0},
	{"Czech Republic (Narrow)", Radio{869.432, 62.5, 7, 5}, 2},
	{"EU 433MHz (Long Range)", Radio{433.650, 250, 11, 5}, 0},
	{"EU 433MHz (Narrow)", Radio{433.650, 62.5, 8, 8}, 0},
	{"Hungary", Radio{869.618, 62.5, 7, 5}, 2},
	{"Netherlands", Radio{869.618, 62.5, 7, 5}, 0},
	{"Netherlands (Limburg)", Radio{869.618, 62.5, 8, 8}, 2},
	{"New Zealand (Narrow)", Radio{917.375, 62.5, 7, 5}, 2},
	{"New Zealand (Gisborne)", Radio{917.375, 250, 11, 5}, 1},
	{"Portugal 433", Radio{433.375, 62.5, 9, 6}, 0},
	{"Portugal 868", Radio{869.618, 62.5, 7, 6}, 0},
	{"Slovakia", Radio{869.618, 62.5, 7, 5}, 2},
	{"Switzerland", Radio{869.618, 62.5, 8, 8}, 0},
	{"Thailand", Radio{921, 62.5, 7, 5}, 0},
	{"USA", Radio{910.525, 62.5, 7, 5}, 0},
	{"USA - Southern California", Radio{927.875, 62.5, 7, 5}, 3},
	{"Vietnam (Narrow)", Radio{920.250, 62.5, 8, 5}, 0},
	{"Vietnam (Deprecated)", Radio{920.250, 250, 11, 5}, 0},
}

// Apply sets s's radio, and its path hash size when the preset gives one, from p.
func (p Preset) Apply(s *Settings) {
	s.Radio = p.Radio
	if p.PathHashBytes != 0 {
		s.PathHashBytes = p.PathHashBytes
	}
}

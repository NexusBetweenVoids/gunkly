package api

import "net/http"

type templateView struct {
	Key      string     `json:"key"`      // name translation key: template.<key>
	Area     string     `json:"area"`     // suggested starter area: area.<key>
	Interval intervalIO `json:"interval"` // suggested repeat
}

// choreTemplates are the starter chore templates the editor offers. The UI
// translates names with "template.<key>" and areas with "area.<area>".
var choreTemplates = []templateView{
	{"wipe_counters", "kitchen", intervalIO{1, "day"}},
	{"clean_stove", "kitchen", intervalIO{1, "week"}},
	{"clean_fridge", "kitchen", intervalIO{1, "month"}},
	{"mop_floors", "general", intervalIO{1, "week"}},
	{"vacuum", "general", intervalIO{1, "week"}},
	{"dust_surfaces", "general", intervalIO{2, "week"}},
	{"change_bedding", "general", intervalIO{2, "week"}},
	{"clean_bathroom", "bathroom", intervalIO{1, "week"}},
	{"clean_shower", "bathroom", intervalIO{2, "week"}},
	{"wash_towels", "bathroom", intervalIO{1, "week"}},
	{"take_out_trash", "kitchen", intervalIO{1, "week"}},
	{"water_plants", "general", intervalIO{1, "week"}},
	{"mow_lawn", "garden", intervalIO{1, "week"}},
	{"prune_hedges", "garden", intervalIO{2, "month"}},
	{"weed_beds", "garden", intervalIO{2, "week"}},
	{"clean_gutters", "exterior", intervalIO{6, "month"}},
	{"wash_windows", "exterior", intervalIO{3, "month"}},
	{"sweep_entrance", "exterior", intervalIO{1, "week"}},
	{"descale_kettle", "appliances", intervalIO{3, "month"}},
	{"clean_dishwasher_filter", "appliances", intervalIO{1, "month"}},
	{"clean_washing_machine", "appliances", intervalIO{3, "month"}},
	{"replace_smoke_detector_batteries", "appliances", intervalIO{12, "month"}},
	{"change_hvac_filter", "appliances", intervalIO{3, "month"}},
}

func (s *Server) listTemplates(w http.ResponseWriter, _ *http.Request, _ *call) error {
	return ok(w, choreTemplates)
}

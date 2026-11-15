package game

// Account option completion, ported from hc.control_options /
// hc.camera_options. A missing or partial controlOption makes the
// client's OptionManager.applyControlSetting return early WITHOUT
// assigning the controller -- the user then controls nobody and the
// AI plays every player (the lobby freeroam "players play by
// themselves" bug of the first Go session).

// ControlOptions completes a saved control profile with the native
// keyboard/pad layout defaults.
func (r *Roster) ControlOptions(saved map[string]any) map[string]any {
	out := map[string]any{}
	if saved == nil {
		saved = map[string]any{}
	}
	for key, def := range r.controlDefaults {
		defList := List(def)
		if defList == nil {
			continue // Controller/Type are handled below, as in Python
		}
		val := List(saved[key])
		valid := len(val) == len(defList)
		if valid {
			for _, n := range val {
				f, isNum := n.(float64)
				if !isNum || f != float64(int(f)) || int(f) < 0 || int(f) > 65535 {
					valid = false
					break
				}
			}
		}
		if valid {
			out[key] = val
		} else {
			cp := make([]any, len(defList))
			copy(cp, defList)
			out[key] = cp
		}
	}
	controller := 0
	if f, isNum := saved["Controller"].(float64); isNum && (f == 0 || f == 1) {
		controller = int(f)
	}
	out["Controller"] = controller
	count := 8
	if controller == 1 {
		count = 3
	}
	typ := 0
	if f, isNum := saved["Type"].(float64); isNum && f == float64(int(f)) && int(f) >= 0 && int(f) < count {
		typ = int(f)
	}
	out["Type"] = typ
	return out
}

// CameraOptions completes legacy camera settings with defaults.
func (r *Roster) CameraOptions(saved map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range r.cameraDefaults {
		out[k] = v
	}
	if saved == nil {
		return out
	}
	for key := range r.cameraDefaults {
		if f, isNum := saved[key].(float64); isNum && f == float64(int(f)) && int(f) >= 0 && int(f) <= 100 {
			out[key] = int(f)
		}
	}
	return out
}

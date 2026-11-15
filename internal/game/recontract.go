package game

// Contract renewal, ported from hc ep_recontract* (preview + apply +
// BASECONTRACTFEE). Fee: max(1, round(ovr*0.13)) per remaining match;
// discount: IGR 20% + VIP 30% (the dialog's discounted figure and
// the charged amount are computed from the same numbers).

import "math"

const (
	RecontractPlayerMaxLife = 40
	// Staff contracts match the player cap (owner directive
	// 2026-11-15: 100 -> 40) and burn one match per match played,
	// exactly like player contracts — see ConsumeStaffContracts.
	RecontractStaffMaxLife = 40
)

// recontractBoostFor builds the boost map for THIS user: the
// IGR (PC) and VIP rebate rates apply only while the matching
// admin-granted entitlement is active.
func recontractBoostFor(st State) map[string]any {
	igr, vip := 0.0, 0.0
	if PCActive(st) {
		igr = CfgF("igr_refund", 0.2)
	}
	if VIPActive(st) {
		vip = CfgF("vip_refund", 0.3)
	}
	return map[string]any{"igr": igr, "vip": vip, "tgp": 0, "discountlimit": 0}
}

// RecontractDiscount is hc._recontract_discount, gated on the
// user's PC/VIP entitlements.
func RecontractDiscount(st State, total int) int {
	igrAmt, vipAmt := 0, 0
	if PCActive(st) {
		igrAmt = int(CfgF("igr_refund", 0.2) * float64(total))
	}
	if VIPActive(st) {
		vipAmt = int(CfgF("vip_refund", 0.3) * float64(total))
	}
	return igrAmt + vipAmt
}

// RecontractCostInfo is hc._recontract_cost_info, plus redundant
// amount/final/balance fields: the stock dialog's discounted-price
// and after-balance lines render blank against the bare
// {total, boost, discount} shape (broken in the Python build too),
// so every plausible binding is fed.
func RecontractCostInfo(st State, total int) map[string]any {
	igrAmt, vipAmt := 0, 0
	if PCActive(st) {
		igrAmt = int(CfgF("igr_refund", 0.2) * float64(total))
	}
	if VIPActive(st) {
		vipAmt = int(CfgF("vip_refund", 0.3) * float64(total))
	}
	discount := igrAmt + vipAmt
	return map[string]any{"total": total, "boost": recontractBoostFor(st),
		"discount": discount, "discountlimit": 0,
		"igr": igrAmt, "vip": vipAmt,
		"discounted": total - discount, "final": total - discount,
		"finalcost": total - discount, "price": total - discount}
}

// RecontractFee is hc._recontract_fee (per remaining matches).
func (r *Roster) RecontractFee(p map[string]any, remaining int) int {
	if remaining <= 0 {
		return 0
	}
	ovr := r.PlayerOvr(p)
	if ovr < 1 {
		ovr = 50
	}
	rate := int(math.RoundToEven(float64(ovr) * CfgF("contract_fee_rate", 0.13)))
	if rate < 1 {
		rate = 1
	}
	return rate * remaining
}

// BaseContractFeeTable is hc.ep_recontract_base_fee: the client's
// own estimate table, at the same per-match rate as the server fee.
func BaseContractFeeTable() map[string]any {
	out := map[string]any{}
	for ovr := 1; ovr <= 200; ovr++ {
		rate := int(math.RoundToEven(float64(ovr) * CfgF("contract_fee_rate", 0.13)))
		if rate < 1 {
			rate = 1
		}
		out[itoa(ovr)] = rate
	}
	return out
}

// RecontractKey is the identity the client addresses a player/staff
// row by: the first truthy of _id, spid, id (hc's by_id keying).
func RecontractKey(p map[string]any) string {
	if v := Str(p["_id"]); v != "" {
		return v
	}
	if p["spid"] != nil && Int(p["spid"]) != 0 {
		return itoa(Int(p["spid"]))
	}
	return Str(p["id"])
}

// StaffLife is hc's staff branch of the renewal life read: the cap
// only when the key is absent; a stored null/0 counts as 0.
func StaffLife(item map[string]any, cap int) int {
	v, present := item["life"]
	if !present {
		return cap
	}
	life := Int(v)
	if life < 0 {
		return 0
	}
	return life
}

// RecontractFeeFor one selected row (player or staff).
func (r *Roster) RecontractFeeFor(item map[string]any, kind string, cap int) int {
	if kind == "player" {
		life := PlayerLife(item)
		if life >= cap {
			return 0
		}
		return r.RecontractFee(item, cap-life)
	}
	life := StaffLife(item, cap)
	if life >= cap {
		return 0
	}
	if fee := Int(item["contractfee"]); fee > 0 {
		return fee
	}
	grade := Int(item["grade"])
	if grade < 1 {
		grade = 1
	}
	return grade * (cap - life)
}

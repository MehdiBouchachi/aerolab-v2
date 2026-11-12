// Package roles is the single authorization matrix (spec section 7.1). Every
// protected endpoint calls Can(); hiding a UI button is never authorization.
package roles

type Role string

const (
	Admin    Role = "ADMIN"
	Operator Role = "OPERATOR"
	Viewer   Role = "VIEWER"
)

type Action string

const (
	ViewData        Action = "view_data"        // site, units, readings, history, incidents
	AckIncident     Action = "ack_incident"     // Admin, Operator
	RecordCrop      Action = "record_crop"      // Admin, Operator (Phase 2)
	EditProgram     Action = "edit_program"     // Admin only
	ManageDevices   Action = "manage_devices"   // Admin only
	ManageUsers     Action = "manage_users"     // Admin only
	ViewAudit       Action = "view_audit"       // Admin only
	ViewDiagnostics Action = "view_diagnostics" // Admin only
)

var matrix = map[Action]map[Role]bool{
	ViewData:        {Admin: true, Operator: true, Viewer: true},
	AckIncident:     {Admin: true, Operator: true},
	RecordCrop:      {Admin: true, Operator: true},
	EditProgram:     {Admin: true},
	ManageDevices:   {Admin: true},
	ManageUsers:     {Admin: true},
	ViewAudit:       {Admin: true},
	ViewDiagnostics: {Admin: true},
}

func Valid(r Role) bool { return r == Admin || r == Operator || r == Viewer }

func Can(r Role, a Action) bool { return matrix[a][r] }

// Capabilities lists what a role may do (returned by /session so clients can
// render honestly; the server still enforces every call).
func Capabilities(r Role) []Action {
	var out []Action
	for _, a := range []Action{ViewData, AckIncident, RecordCrop, EditProgram, ManageDevices, ManageUsers, ViewAudit, ViewDiagnostics} {
		if Can(r, a) {
			out = append(out, a)
		}
	}
	return out
}

# AeroLab web interaction contract

The edge API owns authorization and state changes. The web app displays source, freshness, simulator identity, and pending versus applied programme state explicitly. Navigation between Overview, Alerts, Administration and unit detail uses shared routes; a theme choice persists in local browser storage and is available on login and authenticated screens.

On small screens, the account disclosure is a native details control shown as a bottom sheet. Its summary is keyboard focusable; Escape or a tap on the backdrop closes it, and Escape returns focus to the summary. It closes on navigation. Unit pages show a back link to Overview in the top bar. Main navigation stays visible and route-backed; account controls retain their desktop actions and labels.

Within a unit, `?tab=overview`, `?tab=history`, and `?tab=programme` select native link-based sections. The default is Overview. Changing sections returns the viewport to the top of the unit page. A direct link to either section opens that section without scrolling through the others.

Administration uses URL-backed views under `?view=`. Storage is the default; Controllers, Users, Phone pairing (local only), and Activity are peer views. Switching views resets the page scroll. User search is local to the Users view; role and account status changes wait for the API before refreshing the list.

## Canonical UI Map

| Capability | Canonical owner | Source of truth | Allowed variants | Verification |
|---|---|---|---|---|
| Select/Listbox | Native HTML select | Browser platform | Site and role | Keyboard and popup |
| Form | Page form with shared field CSS | API contract | Login and account creation | Validation and server error |
| Scrollbar | Global application stylesheet | DESIGN.md | Geometry only | Computed style |
| Navigation | Shell and React Router | Routes in App.tsx | Local and cloud | Route checks |
| Status and error | Shared ui.tsx components | API and simulator contracts | Success, warning, critical | Component tests |

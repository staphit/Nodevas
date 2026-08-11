package system

import (
	"context"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/notify"
	"nodevas/internal/project"
)

// PasscodeMailer delivers one-time passcodes. It is an interface rather than
// the concrete sender so a server with no outgoing mail configured holds nil
// here, and so a test can watch what would have been sent without a relay.
type PasscodeMailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// API serves this package's endpoints. It holds only what these
// handlers need, so the router wires each group explicitly.
type API struct {
	pm       *project.ProjectManager
	notifier *notify.Notifier
	auth     auth.Authenticator
	mailer   PasscodeMailer
	audit    *audit.Store
}

// New builds the handler set.
func New(pm *project.ProjectManager, notifier *notify.Notifier, auth auth.Authenticator) *API {
	return &API{pm: pm, notifier: notifier, auth: auth}
}

// UseMailer enables passcode delivery. Without it the passcode endpoint
// answers 503 and says so: a sign-in nobody can complete should fail loudly at
// the door rather than look like a mail that never arrived.
func (a *API) UseMailer(mailer PasscodeMailer) {
	a.mailer = mailer
}

// UseAudit points the trail at the workspace database. Sign-ins and passcode
// requests are recorded there and nowhere else: they belong to no project.
func (a *API) UseAudit(store *audit.Store) {
	a.audit = store
}

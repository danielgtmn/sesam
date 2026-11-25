package sesam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Email is a message sesam or a plugin wants sent, e.g. a password reset
// link. sesam never sends mail itself: the app passes Config.SendEmail and
// delivers it with whatever it already uses.
type Email struct {
	To      string // one address, already normalized
	Subject string
	Text    string // plain text body; always set
	HTML    string // HTML body; may be empty
}

// ErrEmailUnavailable means an action needs to send mail but the app did
// not set Config.SendEmail.
var ErrEmailUnavailable = &Error{http.StatusNotFound, "email_unavailable", "Sending e-mail is not set up here."}

// CanSendEmail tells whether Config.SendEmail is set, so features that need
// mail (and links to them in the app) can be offered.
func (a *Auth) CanSendEmail() bool { return a.cfg.SendEmail != nil }

// SendEmail hands e to Config.SendEmail. Without it, it returns
// ErrEmailUnavailable. An error from the app's function is turned into an
// internal error (logged, never shown), even if it is a *Error.
func (a *Auth) SendEmail(ctx context.Context, e Email) error {
	if a.cfg.SendEmail == nil {
		return ErrEmailUnavailable
	}
	if e.To == "" || e.Subject == "" || e.Text == "" {
		return errors.New("sesam: e-mail needs To, Subject and Text")
	}
	if err := a.cfg.SendEmail(ctx, e); err != nil {
		return fmt.Errorf("sesam: send e-mail: %v", err)
	}
	return nil
}

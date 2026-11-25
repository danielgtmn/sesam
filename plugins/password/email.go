package password

import (
	"bytes"
	"fmt"
	"html/template"
	"time"

	"github.com/danielgtmn/sesam"
)

// ResetEmailData is what a reset e-mail is built from.
type ResetEmailData struct {
	User     sesam.User
	Link     string        // the one-time link built with ResetURL
	ValidFor time.Duration // how long the link works (Options.ResetTTL)
}

var resetHTML = template.Must(template.New("reset").Parse(`<!doctype html>
<html><body style="font-family: sans-serif; line-height: 1.5">
<p>Hello{{if .Name}} {{.Name}}{{end}},</p>
<p>Someone asked to reset the password for this address. To choose a new password, open this link:</p>
<p><a href="{{.Link}}">Choose a new password</a></p>
<p>The link works once and for {{.Valid}}. If you did not ask for this, you can ignore this e-mail; your password stays the same.</p>
</body></html>`))

// DefaultResetEmail is the reset e-mail used unless Options.ResetEmail is
// set: English, plain text and HTML.
func DefaultResetEmail(d ResetEmailData) sesam.Email {
	valid := humanDuration(d.ValidFor)
	hello := "Hello,"
	if d.User.Name != "" {
		hello = "Hello " + d.User.Name + ","
	}
	text := fmt.Sprintf("%s\n\nSomeone asked to reset the password for this address. To choose a new password, open this link:\n\n%s\n\nThe link works once and for %s. If you did not ask for this, you can ignore this e-mail; your password stays the same.\n", hello, d.Link, valid)
	var html bytes.Buffer
	_ = resetHTML.Execute(&html, struct{ Name, Link, Valid string }{d.User.Name, d.Link, valid})
	return sesam.Email{Subject: "Reset your password", Text: text, HTML: html.String()}
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		if d == time.Hour {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", d/time.Hour)
	case d >= time.Minute:
		return fmt.Sprintf("%d minutes", d/time.Minute)
	}
	return d.String()
}

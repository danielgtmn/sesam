// Package password lets people sign in with an e-mail address or a
// username and a password.
//
//	pw := password.New(password.Options{Identifier: password.Username, DisableSignUp: true})
//	auth, _ := sesam.New(sesam.Config{…, Plugins: []sesam.Plugin{pw}})
//	u, token, s, err := pw.SignIn(ctx, password.Credentials{Identifier: "anna", Password: "…"}, auth.Meta(r))
//
// Passwords are hashed with argon2id. Wrong passwords and unknown names give
// the same error and take the same time. Attempts are limited per name and
// per client address.
package password

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/danielgtmn/ablage"

	"github.com/danielgtmn/sesam/v2"
	"github.com/danielgtmn/sesam/v2/internal/argon"
)

// Identifier is what people sign in with.
type Identifier int

const (
	// Email: sign in with the e-mail address; sign-up requires one.
	Email Identifier = iota
	// Username: sign in with the username; sign-up requires one.
	Username
	// EmailOrUsername: sign in with either; a user needs at least one of
	// them. Usernames cannot contain "@", so the two are never confused.
	EmailOrUsername
)

// Provider is the accounts.provider value of password accounts.
const Provider = "credential"

// Purpose of password reset tokens in the verifications table.
const resetPurpose = "password-reset"

// Mailer sends the reset e-mail itself, with a text the app writes.
//
// Deprecated: set sesam.Config.SendEmail instead; the plugin then writes the
// e-mail (see Options.ResetEmail) and the app only delivers it.
type Mailer interface {
	// SendPasswordReset sends link to u.Email; following it lets the person
	// choose a new password.
	SendPasswordReset(ctx context.Context, u sesam.User, link string) error
}

// Options configure the plugin. The zero value is usable: e-mail sign-in,
// sign-up allowed, no password reset.
type Options struct {
	Identifier Identifier
	// DisableSignUp refuses SignUp and its endpoint. CreateUser (for
	// admins and setup) still works.
	DisableSignUp bool

	// MinLength and MaxLength bound passwords, in characters. Default 10
	// and 200.
	MinLength int
	MaxLength int

	// Failed sign-ins allowed within AttemptWindow per name (default 5) and
	// per client address (default 20); then further attempts are refused
	// until the window ends. AttemptWindow defaults to 15 minutes.
	MaxAttemptsPerName   int
	MaxAttemptsPerClient int
	AttemptWindow        time.Duration

	// ResetURL enables password reset, together with sesam.Config.SendEmail.
	// It builds the link from the one-time token, e.g.
	// func(t string) string { return "https://app.example.com/reset?token=" + t }.
	ResetURL func(token string) string
	// ResetEmail writes the reset e-mail (To is filled in). Default
	// DefaultResetEmail: English, plain text and HTML.
	ResetEmail func(ResetEmailData) sesam.Email
	// Mailer, if set, sends the reset e-mail instead of Config.SendEmail.
	//
	// Deprecated: use sesam.Config.SendEmail.
	Mailer Mailer
	// ResetTTL is how long a reset link works. Default 1 hour.
	ResetTTL time.Duration

	// HashConcurrency bounds how many passwords are hashed at once; each
	// takes 64 MiB. Default 4.
	HashConcurrency int
}

// Plugin is the password plugin. Create it with New.
type Plugin struct {
	o     Options
	a     *sesam.Auth
	slots argon.Slots
	dummy func() string
}

// New returns the plugin.
func New(o Options) *Plugin {
	if o.MinLength <= 0 {
		o.MinLength = 10
	}
	if o.MaxLength <= 0 {
		o.MaxLength = 200
	}
	if o.MaxAttemptsPerName <= 0 {
		o.MaxAttemptsPerName = 5
	}
	if o.MaxAttemptsPerClient <= 0 {
		o.MaxAttemptsPerClient = 20
	}
	if o.AttemptWindow <= 0 {
		o.AttemptWindow = 15 * time.Minute
	}
	if o.ResetTTL <= 0 {
		o.ResetTTL = time.Hour
	}
	if o.HashConcurrency <= 0 {
		o.HashConcurrency = 4
	}
	p := &Plugin{o: o, slots: argon.NewSlots(o.HashConcurrency)}
	// Verified against when a user has no password, so that takes as long
	// as a wrong password.
	p.dummy = sync.OnceValue(func() string {
		h, err := p.slots.Hash(sesam.NewToken())
		if err != nil {
			panic(err)
		}
		return h
	})
	return p
}

func (p *Plugin) ID() string { return "password" }

func (p *Plugin) Init(a *sesam.Auth) error {
	if p.o.Identifier != Email && p.o.Identifier != Username && p.o.Identifier != EmailOrUsername {
		return errors.New("invalid Identifier")
	}
	if p.o.MinLength > p.o.MaxLength {
		return errors.New("MinLength is greater than MaxLength")
	}
	if p.o.Mailer != nil && p.o.ResetURL == nil {
		return errors.New("Mailer needs ResetURL")
	}
	if p.o.ResetURL != nil && p.o.Mailer == nil && !a.CanSendEmail() {
		return errors.New("ResetURL needs sesam.Config.SendEmail to deliver the e-mail")
	}
	if p.o.ResetEmail == nil {
		p.o.ResetEmail = DefaultResetEmail
	}
	p.a = a
	return nil
}

// Credentials are what a person signs in with. Identifier is the e-mail
// address or username, depending on Options.Identifier (with
// EmailOrUsername, either).
type Credentials struct {
	Identifier string
	Password   string
}

// SignUpInput creates an account. Depending on Options.Identifier, Email or
// Username is required and the other is optional; with EmailOrUsername, one
// of them is enough.
type SignUpInput struct {
	Email    string
	Username string
	Name     string
	Password string
}

// ValidatePassword checks a new password against the length rules.
func (p *Plugin) ValidatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	if n < p.o.MinLength || n > p.o.MaxLength {
		return sesam.InvalidInput("The password must be between %d and %d characters long.", p.o.MinLength, p.o.MaxLength)
	}
	return nil
}

func (p *Plugin) hash(pw string) (string, error) {
	if err := p.ValidatePassword(pw); err != nil {
		return "", err
	}
	return p.slots.Hash(pw)
}

func (p *Plugin) requireIdentifier(email, username string) error {
	if p.o.Identifier == Email && strings.TrimSpace(email) == "" {
		return sesam.InvalidInput("Enter an e-mail address.")
	}
	if p.o.Identifier == Username && strings.TrimSpace(username) == "" {
		return sesam.InvalidInput("Enter a name.")
	}
	return nil
}

// SignUp creates an account with a password and signs it in. It fails with
// sesam.ErrSignUpDisabled if Options.DisableSignUp is set.
func (p *Plugin) SignUp(ctx context.Context, in SignUpInput, m sesam.SessionMeta) (sesam.User, string, sesam.Session, error) {
	if p.o.DisableSignUp {
		return sesam.User{}, "", sesam.Session{}, sesam.ErrSignUpDisabled
	}
	u, err := p.CreateUser(ctx, sesam.NewUser{Email: in.Email, Username: in.Username, Name: in.Name}, in.Password)
	if err != nil {
		return sesam.User{}, "", sesam.Session{}, err
	}
	token, s, err := p.a.SignIn(ctx, u, m)
	if err != nil {
		return sesam.User{}, "", sesam.Session{}, err
	}
	return u, token, s, nil
}

// CreateUser creates a user with a password without signing in, e.g. for
// an admin or the first-run setup. It works even with DisableSignUp.
func (p *Plugin) CreateUser(ctx context.Context, nu sesam.NewUser, password string) (sesam.User, error) {
	if err := p.requireIdentifier(nu.Email, nu.Username); err != nil {
		return sesam.User{}, err
	}
	h, err := p.hash(password)
	if err != nil {
		return sesam.User{}, err
	}
	return p.a.CreateUserTx(ctx, nu, func(tx ablage.Store, u sesam.User) error {
		return p.createAccount(ctx, p.a.Accounts().With(tx), u.ID, h)
	})
}

func (p *Plugin) createAccount(ctx context.Context, accounts *ablage.Repository[sesam.Account], userID, hash string) error {
	now := ablage.NormalizeTime(p.a.Now())
	return accounts.Create(ctx, &sesam.Account{
		ID: sesam.NewID(), UserID: userID, Provider: Provider, ProviderAccountID: userID,
		PasswordHash: &hash, CreatedAt: now, UpdatedAt: now,
	})
}

// whereAccount selects the password account of a user.
func whereAccount(userID string) ablage.Cond {
	return ablage.And(ablage.Eq("provider", Provider), ablage.Eq("provider_account_id", userID))
}

// passwordHash returns the stored hash of a user; ablage.ErrNotFound if
// the user has no password.
func (p *Plugin) passwordHash(ctx context.Context, userID string) (string, error) {
	acc, err := p.a.Accounts().First(ctx, ablage.Query{Where: whereAccount(userID)})
	if err != nil {
		return "", err
	}
	if acc.PasswordHash == nil || *acc.PasswordHash == "" {
		return "", fmt.Errorf("password: %w: account without hash", ablage.ErrNotFound)
	}
	return *acc.PasswordHash, nil
}

func (p *Plugin) lookup(ctx context.Context, identifier string) (sesam.User, error) {
	switch {
	case p.o.Identifier == Email,
		p.o.Identifier == EmailOrUsername && strings.Contains(identifier, "@"):
		return p.a.UserByEmail(ctx, identifier)
	}
	return p.a.UserByUsername(ctx, identifier)
}

// SignIn checks the credentials and creates a session. Wrong or unknown
// credentials give sesam.ErrInvalidCredentials; too many attempts a
// *sesam.RateLimitError.
func (p *Plugin) SignIn(ctx context.Context, c Credentials, m sesam.SessionMeta) (sesam.User, string, sesam.Session, error) {
	fail := func(err error) (sesam.User, string, sesam.Session, error) {
		return sesam.User{}, "", sesam.Session{}, err
	}
	name := strings.ToLower(strings.TrimSpace(c.Identifier))
	if m.IP != "" {
		if err := p.a.Limit(ctx, "password:sign-in:client:"+m.IP, p.o.MaxAttemptsPerClient, p.o.AttemptWindow); err != nil {
			return fail(err)
		}
	}
	u, err := p.lookup(ctx, name)
	if err != nil && !errors.Is(err, sesam.ErrNotFound) {
		return fail(err)
	}
	// Failures count per account, so trying the e-mail address and the
	// username of one account shares a single limit. Unknown names count
	// on their own.
	nameKey := "password:sign-in:name:" + name
	if err == nil {
		nameKey = "password:sign-in:user:" + u.ID
	}
	if err := p.a.Limit(ctx, nameKey, p.o.MaxAttemptsPerName, p.o.AttemptWindow); err != nil {
		return fail(err)
	}
	if len(c.Password) > 4*p.o.MaxLength {
		return fail(sesam.ErrInvalidCredentials)
	}
	hash := ""
	if err == nil {
		hash, err = p.passwordHash(ctx, u.ID)
		if err != nil && !errors.Is(err, ablage.ErrNotFound) {
			return fail(err)
		}
	}
	if hash == "" {
		p.slots.Verify(p.dummy(), c.Password)
		return fail(sesam.ErrInvalidCredentials)
	}
	if !p.slots.Verify(hash, c.Password) {
		return fail(sesam.ErrInvalidCredentials)
	}
	if err := p.a.ResetLimit(ctx, nameKey); err != nil {
		return fail(err)
	}
	// Only failures count against the client address: many people may
	// sign in from one address.
	if m.IP != "" {
		if err := p.a.UndoLimit(ctx, "password:sign-in:client:"+m.IP); err != nil {
			return fail(err)
		}
	}
	token, s, err := p.a.SignIn(ctx, u, m)
	if err != nil {
		return fail(err)
	}
	return u, token, s, nil
}

// VerifyPassword checks the current password of a signed-in user, e.g.
// before changing the e-mail address. A wrong password, or a user without
// one, gives sesam.ErrWrongPassword; failures are limited per user like in
// ChangePassword and share that limit.
func (p *Plugin) VerifyPassword(ctx context.Context, userID, password string) error {
	if err := p.a.Limit(ctx, "password:change:"+userID, p.o.MaxAttemptsPerName, p.o.AttemptWindow); err != nil {
		return err
	}
	hash, err := p.passwordHash(ctx, userID)
	if errors.Is(err, ablage.ErrNotFound) {
		p.slots.Verify(p.dummy(), password)
		return sesam.ErrWrongPassword
	}
	if err != nil {
		return err
	}
	if len(password) > 4*p.o.MaxLength || !p.slots.Verify(hash, password) {
		return sesam.ErrWrongPassword
	}
	return p.a.ResetLimit(ctx, "password:change:"+userID)
}

// ChangePassword sets a new password after checking the current one, and
// ends the user's other sessions (all except keepSessionID).
func (p *Plugin) ChangePassword(ctx context.Context, userID, current, next, keepSessionID string) error {
	if err := p.VerifyPassword(ctx, userID, current); err != nil {
		return err
	}
	h, err := p.hash(next)
	if err != nil {
		return err
	}
	if err := p.storeHash(ctx, userID, h); err != nil {
		return err
	}
	_, err = p.a.RevokeUserSessions(ctx, userID, keepSessionID)
	return err
}

// SetPassword sets a password without knowing the old one (admins, reset)
// and ends all sessions of the user. A user without a password gets one.
func (p *Plugin) SetPassword(ctx context.Context, userID, password string) error {
	h, err := p.hash(password)
	if err != nil {
		return err
	}
	return p.setHash(ctx, userID, h)
}

// SetPasswordHash stores an existing argon2id hash in the format
// "$argon2id$v=19$m=…,t=…,p=…$salt$key", e.g. when moving users from
// another system, and ends all sessions of the user.
func (p *Plugin) SetPasswordHash(ctx context.Context, userID, hash string) error {
	if !argon.WellFormed(hash) {
		return sesam.InvalidInput("This is not a password hash that can be imported.")
	}
	return p.setHash(ctx, userID, hash)
}

func (p *Plugin) setHash(ctx context.Context, userID, h string) error {
	if _, err := p.a.User(ctx, userID); err != nil {
		return err
	}
	if err := p.storeHash(ctx, userID, h); err != nil {
		return err
	}
	_, err := p.a.RevokeUserSessions(ctx, userID, "")
	return err
}

// storeHash updates the password account, or creates it.
func (p *Plugin) storeHash(ctx context.Context, userID, h string) error {
	n, err := p.a.Store().Update(ctx, sesam.TableAccounts,
		ablage.Row{"password_hash": h, "updated_at": ablage.NormalizeTime(p.a.Now())}, whereAccount(userID))
	if err != nil || n > 0 {
		return err
	}
	return p.createAccount(ctx, p.a.Accounts(), userID, h)
}

// HasPassword tells whether the user can sign in with a password.
func (p *Plugin) HasPassword(ctx context.Context, userID string) (bool, error) {
	_, err := p.passwordHash(ctx, userID)
	if errors.Is(err, ablage.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// ResetEnabled tells whether password reset is offered: ResetURL is set and
// e-mail can be sent. Apps show "forgot password" only then.
func (p *Plugin) ResetEnabled() bool {
	return p.o.ResetURL != nil && (p.o.Mailer != nil || p.a.CanSendEmail())
}

// RequestReset sends a reset link to the e-mail address if it belongs to a
// user. To not reveal which addresses are known, it returns nil for unknown
// addresses too. Requests are limited per address and per client.
func (p *Plugin) RequestReset(ctx context.Context, email string, m sesam.SessionMeta) error {
	if !p.ResetEnabled() {
		return &sesam.Error{Status: 404, Code: "reset_disabled", Message: "Passwords cannot be reset here."}
	}
	e, err := sesam.NormalizeEmail(email)
	if err != nil {
		return err
	}
	if m.IP != "" {
		if err := p.a.Limit(ctx, "password:reset:client:"+m.IP, p.o.MaxAttemptsPerClient, time.Hour); err != nil {
			return err
		}
	}
	if err := p.a.Limit(ctx, "password:reset:email:"+e, 3, time.Hour); err != nil {
		return err
	}
	u, err := p.a.UserByEmail(ctx, e)
	if errors.Is(err, sesam.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	token, err := p.a.CreateVerification(ctx, resetPurpose, u.ID, p.o.ResetTTL)
	if err != nil {
		return err
	}
	link := p.o.ResetURL(token)
	if p.o.Mailer != nil {
		if err := p.o.Mailer.SendPasswordReset(ctx, u, link); err != nil {
			return fmt.Errorf("password: send reset mail: %v", err)
		}
		return nil
	}
	msg := p.o.ResetEmail(ResetEmailData{User: u, Link: link, ValidFor: p.o.ResetTTL})
	msg.To = u.Email
	return p.a.SendEmail(ctx, msg)
}

// ResetPassword sets a new password with a token from a reset link and
// returns the user it belongs to. The token works once; all sessions of the
// user end. Following the link proves the person can read the address, so
// it counts as verified.
func (p *Plugin) ResetPassword(ctx context.Context, token, password string) (sesam.User, error) {
	if err := p.ValidatePassword(password); err != nil {
		return sesam.User{}, err
	}
	userID, err := p.a.ConsumeVerification(ctx, resetPurpose, token)
	if err != nil {
		return sesam.User{}, err
	}
	if err := p.SetPassword(ctx, userID, password); err != nil {
		return sesam.User{}, err
	}
	verified := true
	return p.a.UpdateUser(ctx, userID, sesam.UserUpdate{EmailVerified: &verified})
}

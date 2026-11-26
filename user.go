package sesam

import (
	"context"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtmn/ablage"
)

// User is a person who can sign in. Email and Username are optional, but a
// user always has at least one of them.
type User struct {
	ID            string    `json:"id"`
	Email         string    `json:"email,omitempty"`
	EmailVerified bool      `json:"email_verified"`
	Username      string    `json:"username,omitempty"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// NewUser is the input for CreateUser.
type NewUser struct {
	Email         string
	EmailVerified bool
	Username      string
	Name          string
}

// UserUpdate changes the fields that are not nil. Changing the e-mail
// address marks it as not verified unless EmailVerified is set as well. An
// empty string removes the e-mail address or username, as long as the
// other remains.
type UserUpdate struct {
	Email         *string
	EmailVerified *bool
	Username      *string
	Name          *string
}

// ListOptions page through a list. Limit 0 means 100.
type ListOptions struct {
	Limit  int
	Offset int
}

// NormalizeEmail trims and lower-cases an e-mail address and checks that it
// is a plain address (no display name).
func NormalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s || len(s) > 254 || !strings.Contains(s[strings.LastIndex(s, "@")+1:], ".") {
		return "", InvalidInput("Enter a valid e-mail address.")
	}
	return s, nil
}

var usernameRe = regexp.MustCompile(`^[a-z0-9._-]{3,32}$`)

// NormalizeUsername trims and lower-cases a username and checks it: 3 to 32
// characters, letters a-z, digits, ".", "_" and "-".
func NormalizeUsername(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !usernameRe.MatchString(s) {
		return "", InvalidInput("A name must be 3 to 32 characters long and may contain only letters, digits, dots, dashes and underscores.")
	}
	return s, nil
}

func normalizeName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 200 || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", InvalidInput("The name is too long or contains invalid characters.")
	}
	return s, nil
}

func (r userRow) user() User {
	return User{
		ID:            r.ID,
		Email:         deref(r.Email),
		EmailVerified: r.EmailVerified,
		Username:      deref(r.Username),
		Name:          r.Name,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nullable stores "" as NULL.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// CreateUser creates a user without any way to sign in; plugins add one
// (password.CreateUser sets a password in the same step).
func (a *Auth) CreateUser(ctx context.Context, nu NewUser) (User, error) {
	return a.CreateUserTx(ctx, nu, nil)
}

// CreateUserTx creates a user and runs fn in the same transaction, so a
// plugin can add the user's account atomically: inside fn, use tx (e.g.
// a.Accounts().With(tx)), never the outer store. Hooks run before the
// transaction starts and after it commits.
func (a *Auth) CreateUserTx(ctx context.Context, nu NewUser, fn func(tx ablage.Store, u User) error) (User, error) {
	for _, h := range a.allHooks() {
		if h.BeforeCreateUser != nil {
			if err := h.BeforeCreateUser(ctx, &nu); err != nil {
				return User{}, err
			}
		}
	}
	var err error
	if nu.Email != "" {
		if nu.Email, err = NormalizeEmail(nu.Email); err != nil {
			return User{}, err
		}
	}
	if nu.Username != "" {
		if nu.Username, err = NormalizeUsername(nu.Username); err != nil {
			return User{}, err
		}
	}
	if nu.Email == "" && nu.Username == "" {
		return User{}, InvalidInput("Enter an e-mail address or a name.")
	}
	if nu.Name, err = normalizeName(nu.Name); err != nil {
		return User{}, err
	}
	now := ablage.NormalizeTime(a.Now())
	u := User{
		ID: NewID(), Email: nu.Email, EmailVerified: nu.EmailVerified && nu.Email != "",
		Username: nu.Username, Name: nu.Name, CreatedAt: now, UpdatedAt: now,
	}
	err = a.db.Tx(ctx, func(tx ablage.Store) error {
		if err := a.r.users.With(tx).Create(ctx, &userRow{
			ID: u.ID, Email: nullable(u.Email), EmailVerified: u.EmailVerified,
			Username: nullable(u.Username), Name: u.Name, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if fn != nil {
			return fn(tx, u)
		}
		return nil
	})
	if err != nil {
		return User{}, a.takenError(ctx, err, u.Email, u.Username)
	}
	for _, h := range a.allHooks() {
		if h.AfterCreateUser != nil {
			h.AfterCreateUser(ctx, u)
		}
	}
	return u, nil
}

// takenError turns a duplicate into the error that says which value is
// taken. Other errors pass through.
func (a *Auth) takenError(ctx context.Context, err error, email, username string) error {
	if !errors.Is(err, ablage.ErrDuplicate) {
		return err
	}
	if email != "" {
		if ok, _ := a.exists(ctx, ablage.Eq("email", email)); ok {
			return ErrEmailTaken
		}
	}
	if username != "" {
		if ok, _ := a.exists(ctx, ablage.Eq("username", username)); ok {
			return ErrUsernameTaken
		}
	}
	return err
}

func (a *Auth) exists(ctx context.Context, where ablage.Cond) (bool, error) {
	n, err := a.r.users.Count(ctx, where)
	return n > 0, err
}

func (a *Auth) findUser(ctx context.Context, where ablage.Cond) (User, error) {
	r, err := a.r.users.First(ctx, ablage.Query{Where: where})
	if errors.Is(err, ablage.ErrNotFound) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return r.user(), nil
}

// User returns the user with the given id, or ErrNotFound.
func (a *Auth) User(ctx context.Context, id string) (User, error) {
	return a.findUser(ctx, ablage.Eq("id", id))
}

// UserByEmail returns the user with the e-mail address, or ErrNotFound.
func (a *Auth) UserByEmail(ctx context.Context, email string) (User, error) {
	e, err := NormalizeEmail(email)
	if err != nil {
		return User{}, ErrNotFound
	}
	return a.findUser(ctx, ablage.Eq("email", e))
}

// UserByUsername returns the user with the username, or ErrNotFound.
func (a *Auth) UserByUsername(ctx context.Context, username string) (User, error) {
	n, err := NormalizeUsername(username)
	if err != nil {
		return User{}, ErrNotFound
	}
	return a.findUser(ctx, ablage.Eq("username", n))
}

// Users lists users, oldest first.
func (a *Auth) Users(ctx context.Context, o ListOptions) ([]User, error) {
	if o.Limit <= 0 {
		o.Limit = 100
	}
	rows, err := a.r.users.Find(ctx, ablage.Query{
		OrderBy: []ablage.Order{ablage.Asc("created_at")}, Limit: o.Limit, Offset: o.Offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]User, len(rows))
	for i, r := range rows {
		out[i] = r.user()
	}
	return out, nil
}

// CountUsers returns the number of users, e.g. to find out whether an app
// still needs its first account.
func (a *Auth) CountUsers(ctx context.Context) (int64, error) {
	return a.r.users.Count(ctx, ablage.All)
}

// UpdateUser changes a user and returns the result.
func (a *Auth) UpdateUser(ctx context.Context, id string, up UserUpdate) (User, error) {
	u, err := a.User(ctx, id)
	if err != nil {
		return User{}, err
	}
	set := ablage.Row{}
	if up.Email != nil {
		e := ""
		if *up.Email != "" {
			if e, err = NormalizeEmail(*up.Email); err != nil {
				return User{}, err
			}
		}
		if e != u.Email {
			set["email"] = nullable(e)
			set["email_verified"] = false
			u.Email, u.EmailVerified = e, false
		}
	}
	if up.EmailVerified != nil {
		u.EmailVerified = *up.EmailVerified && u.Email != ""
		set["email_verified"] = u.EmailVerified
	}
	if up.Username != nil {
		n := ""
		if *up.Username != "" {
			if n, err = NormalizeUsername(*up.Username); err != nil {
				return User{}, err
			}
		}
		set["username"] = nullable(n)
		u.Username = n
	}
	if up.Name != nil {
		if u.Name, err = normalizeName(*up.Name); err != nil {
			return User{}, err
		}
		set["name"] = u.Name
	}
	if u.Email == "" && u.Username == "" {
		return User{}, InvalidInput("A user needs an e-mail address or a name.")
	}
	if len(set) == 0 {
		return u, nil
	}
	u.UpdatedAt = ablage.NormalizeTime(a.Now())
	set["updated_at"] = u.UpdatedAt
	if err := a.r.users.Patch(ctx, id, set); err != nil {
		if errors.Is(err, ablage.ErrNotFound) {
			return User{}, ErrNotFound
		}
		return User{}, a.takenError(ctx, err, deref(stringIf(set, "email")), deref(stringIf(set, "username")))
	}
	return u, nil
}

func stringIf(r ablage.Row, k string) *string { s, _ := r[k].(*string); return s }

// DeleteUser deletes a user with everything that refers to it: sessions,
// accounts, one-time tokens, and rows of plugin tables whose reference to
// users cascades.
func (a *Auth) DeleteUser(ctx context.Context, id string) error {
	u, err := a.User(ctx, id)
	if err != nil {
		return err
	}
	for _, h := range a.allHooks() {
		if h.BeforeDeleteUser != nil {
			if err := h.BeforeDeleteUser(ctx, u); err != nil {
				return err
			}
		}
	}
	err = a.db.Tx(ctx, func(tx ablage.Store) error {
		if _, err := a.r.sessions.With(tx).DeleteWhere(ctx, ablage.Eq("user_id", id)); err != nil {
			return err
		}
		if _, err := a.r.accounts.With(tx).DeleteWhere(ctx, ablage.Eq("user_id", id)); err != nil {
			return err
		}
		// Unlike sessions, verifications point at users by identifier.
		if _, err := a.r.verifications.With(tx).DeleteWhere(ctx, ablage.Eq("identifier", id)); err != nil {
			return err
		}
		return a.r.users.With(tx).Delete(ctx, id)
	})
	if errors.Is(err, ablage.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	for _, h := range a.allHooks() {
		if h.AfterDeleteUser != nil {
			h.AfterDeleteUser(ctx, u)
		}
	}
	return nil
}

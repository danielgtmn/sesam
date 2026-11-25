package sesam

import (
	"context"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtmn/sesam/adapter"
	"github.com/danielgtmn/sesam/schema"
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

func userFromRow(r adapter.Row) User {
	return User{
		ID:            r.String("id"),
		Email:         r.String("email"),
		EmailVerified: r.Bool("email_verified"),
		Username:      r.String("username"),
		Name:          r.String("name"),
		CreatedAt:     r.Time("created_at"),
		UpdatedAt:     r.Time("updated_at"),
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// CreateUser creates a user without any way to sign in; plugins add one
// (password.CreateUser sets a password in the same step).
func (a *Auth) CreateUser(ctx context.Context, nu NewUser) (User, error) {
	return a.CreateUserTx(ctx, nu, nil)
}

// CreateUserTx creates a user and runs fn in the same transaction, so a
// plugin can add the user's account atomically. Hooks run before the
// transaction starts and after it commits.
func (a *Auth) CreateUserTx(ctx context.Context, nu NewUser, fn func(tx adapter.Adapter, u User) error) (User, error) {
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
	now := adapter.NormalizeTime(a.Now())
	u := User{
		ID: NewID(), Email: nu.Email, EmailVerified: nu.EmailVerified && nu.Email != "",
		Username: nu.Username, Name: nu.Name, CreatedAt: now, UpdatedAt: now,
	}
	err = a.db.Tx(ctx, func(tx adapter.Adapter) error {
		if err := tx.Create(ctx, TableUsers, adapter.Row{
			"id": u.ID, "email": nullable(u.Email), "email_verified": u.EmailVerified,
			"username": nullable(u.Username), "name": u.Name, "created_at": now, "updated_at": now,
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

// takenError turns a conflict into the error that says which value is
// taken. Other errors pass through.
func (a *Auth) takenError(ctx context.Context, err error, email, username string) error {
	if !errors.Is(err, adapter.ErrConflict) {
		return err
	}
	if email != "" {
		if _, e := a.db.FindOne(ctx, TableUsers, adapter.Eq("email", email)); e == nil {
			return ErrEmailTaken
		}
	}
	if username != "" {
		if _, e := a.db.FindOne(ctx, TableUsers, adapter.Eq("username", username)); e == nil {
			return ErrUsernameTaken
		}
	}
	return err
}

func (a *Auth) findUser(ctx context.Context, where adapter.Where) (User, error) {
	r, err := a.db.FindOne(ctx, TableUsers, where)
	if errors.Is(err, adapter.ErrNotFound) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return userFromRow(r), nil
}

// User returns the user with the given id, or ErrNotFound.
func (a *Auth) User(ctx context.Context, id string) (User, error) {
	return a.findUser(ctx, adapter.Eq("id", id))
}

// UserByEmail returns the user with the e-mail address, or ErrNotFound.
func (a *Auth) UserByEmail(ctx context.Context, email string) (User, error) {
	e, err := NormalizeEmail(email)
	if err != nil {
		return User{}, ErrNotFound
	}
	return a.findUser(ctx, adapter.Eq("email", e))
}

// UserByUsername returns the user with the username, or ErrNotFound.
func (a *Auth) UserByUsername(ctx context.Context, username string) (User, error) {
	n, err := NormalizeUsername(username)
	if err != nil {
		return User{}, ErrNotFound
	}
	return a.findUser(ctx, adapter.Eq("username", n))
}

// Users lists users, oldest first.
func (a *Auth) Users(ctx context.Context, o ListOptions) ([]User, error) {
	if o.Limit <= 0 {
		o.Limit = 100
	}
	rows, err := a.db.FindMany(ctx, TableUsers, adapter.Query{OrderBy: "created_at", Limit: o.Limit, Offset: o.Offset})
	if err != nil {
		return nil, err
	}
	out := make([]User, len(rows))
	for i, r := range rows {
		out[i] = userFromRow(r)
	}
	return out, nil
}

// CountUsers returns the number of users, e.g. to find out whether an app
// still needs its first account.
func (a *Auth) CountUsers(ctx context.Context) (int64, error) {
	return a.db.Count(ctx, TableUsers)
}

// UpdateUser changes a user and returns the result.
func (a *Auth) UpdateUser(ctx context.Context, id string, up UserUpdate) (User, error) {
	u, err := a.User(ctx, id)
	if err != nil {
		return User{}, err
	}
	set := adapter.Row{}
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
	u.UpdatedAt = adapter.NormalizeTime(a.Now())
	set["updated_at"] = u.UpdatedAt
	if _, err := a.db.Update(ctx, TableUsers, set, adapter.Eq("id", id)); err != nil {
		return User{}, a.takenError(ctx, err, stringIf(set, "email"), stringIf(set, "username"))
	}
	return u, nil
}

func stringIf(r adapter.Row, k string) string { s, _ := r[k].(string); return s }

// DeleteUser deletes a user with everything that refers to it: sessions,
// accounts, and rows of plugin tables that reference the user.
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
	err = a.db.Tx(ctx, func(tx adapter.Adapter) error {
		if err := a.deleteDependents(ctx, tx, TableUsers, []any{id}, 0); err != nil {
			return err
		}
		// Unlike sessions, verifications point at users by identifier.
		if _, err := tx.Delete(ctx, TableVerifications, adapter.Eq("identifier", id)); err != nil {
			return err
		}
		_, err := tx.Delete(ctx, TableUsers, adapter.Eq("id", id))
		return err
	})
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

// deleteDependents deletes, depth first, every row that references one of
// ids in table.
func (a *Auth) deleteDependents(ctx context.Context, tx adapter.Adapter, table string, ids []any, depth int) error {
	if depth > 8 || len(ids) == 0 {
		return nil
	}
	for _, d := range schema.Dependents(a.tables, table) {
		rows, err := tx.FindMany(ctx, d.Table, adapter.Query{Where: []adapter.Where{adapter.In(d.Field, ids...)}})
		if err != nil {
			return err
		}
		child := make([]any, len(rows))
		for i, r := range rows {
			child[i] = r.String("id")
		}
		if err := a.deleteDependents(ctx, tx, d.Table, child, depth+1); err != nil {
			return err
		}
		if _, err := tx.Delete(ctx, d.Table, adapter.In(d.Field, ids...)); err != nil {
			return err
		}
	}
	return nil
}

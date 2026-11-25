package sesam

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Error is an error meant to be shown to the person using the app. Message
// is a readable sentence; Code is stable and meant for programs.
type Error struct {
	Status  int    // HTTP status the handler answers with
	Code    string // e.g. "invalid_credentials"
	Message string
}

func (e *Error) Error() string { return e.Message }

// Is matches errors with the same Code, so errors.Is(err, ErrNotFound) holds
// for every not-found error, whatever its message.
func (e *Error) Is(target error) bool {
	var t *Error
	return errors.As(target, &t) && t.Code == e.Code
}

// Errors returned by sesam and its plugins.
var (
	ErrInvalidCredentials = &Error{http.StatusUnauthorized, "invalid_credentials", "The name or password is wrong."}
	ErrUnauthenticated    = &Error{http.StatusUnauthorized, "unauthenticated", "You are not signed in."}
	ErrWrongPassword      = &Error{http.StatusForbidden, "wrong_password", "The current password is wrong."}
	ErrNotFound           = &Error{http.StatusNotFound, "not_found", "Not found."}
	ErrEmailTaken         = &Error{http.StatusConflict, "email_taken", "This e-mail address is already in use."}
	ErrUsernameTaken      = &Error{http.StatusConflict, "username_taken", "This name is already taken."}
	ErrSignUpDisabled     = &Error{http.StatusForbidden, "sign_up_disabled", "New accounts cannot be created here."}
	ErrForbiddenOrigin    = &Error{http.StatusForbidden, "forbidden_origin", "This request was not sent from a trusted page."}
	ErrInvalidToken       = &Error{http.StatusBadRequest, "invalid_token", "This link is invalid or has expired."}
)

// InvalidInput returns an error for input that cannot be accepted, with a
// sentence that says what is wrong.
func InvalidInput(format string, args ...any) *Error {
	return &Error{http.StatusBadRequest, "invalid_input", fmt.Sprintf(format, args...)}
}

// ErrInvalidInput matches every error from InvalidInput.
var ErrInvalidInput = &Error{Code: "invalid_input"}

// RateLimitError says that too many attempts were made; it matches
// ErrRateLimited.
type RateLimitError struct {
	RetryAfter time.Duration
}

// ErrRateLimited matches every *RateLimitError.
var ErrRateLimited = &Error{http.StatusTooManyRequests, "rate_limited", "Too many attempts."}

func (e *RateLimitError) Error() string {
	minutes := int((e.RetryAfter + time.Minute - 1) / time.Minute)
	if minutes <= 1 {
		return "Too many attempts. Try again in a minute."
	}
	return fmt.Sprintf("Too many attempts. Try again in %d minutes.", minutes)
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

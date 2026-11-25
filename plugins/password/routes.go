package password

import (
	"net/http"

	"github.com/danielgtmn/sesam"
)

// Routes registers the endpoints under Config.BasePath:
//
//	POST /sign-up                 {email, username, name, password}  201, sets the cookie
//	POST /sign-in                 {email|username, password}         200, sets the cookie
//	POST /change-password         {current, new}                     204, signed in
//	POST /request-password-reset  {email}                            204
//	POST /reset-password          {token, password}                  204
func (p *Plugin) Routes(r *sesam.Router) {
	r.Handle("POST", "/sign-up", p.handleSignUp)
	r.Handle("POST", "/sign-in", p.handleSignIn)
	r.Handle("POST", "/change-password", p.handleChangePassword)
	r.Handle("POST", "/request-password-reset", p.handleRequestReset)
	r.Handle("POST", "/reset-password", p.handleReset)
}

func (p *Plugin) handleSignUp(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := sesam.ReadJSON(r, &in); err != nil {
		return err
	}
	u, token, s, err := p.SignUp(r.Context(), SignUpInput(in), p.a.Meta(r))
	if err != nil {
		return err
	}
	p.a.SetSessionCookie(w, token)
	sesam.WriteJSON(w, http.StatusCreated, map[string]any{"user": u, "session": s})
	return nil
}

func (p *Plugin) handleSignIn(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := sesam.ReadJSON(r, &in); err != nil {
		return err
	}
	id := in.Email
	if p.o.Identifier == Username {
		id = in.Username
	}
	u, token, s, err := p.SignIn(r.Context(), Credentials{Identifier: id, Password: in.Password}, p.a.Meta(r))
	if err != nil {
		return err
	}
	p.a.SetSessionCookie(w, token)
	sesam.WriteJSON(w, http.StatusOK, map[string]any{"user": u, "session": s})
	return nil
}

func (p *Plugin) handleChangePassword(w http.ResponseWriter, r *http.Request) error {
	s, u, err := p.a.SessionFromRequest(r)
	if err != nil {
		return err
	}
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := sesam.ReadJSON(r, &in); err != nil {
		return err
	}
	if err := p.ChangePassword(r.Context(), u.ID, in.Current, in.New, s.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (p *Plugin) handleRequestReset(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email string `json:"email"`
	}
	if err := sesam.ReadJSON(r, &in); err != nil {
		return err
	}
	if err := p.RequestReset(r.Context(), in.Email, p.a.Meta(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (p *Plugin) handleReset(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := sesam.ReadJSON(r, &in); err != nil {
		return err
	}
	if err := p.ResetPassword(r.Context(), in.Token, in.Password); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

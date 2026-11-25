// Command basic is a minimal app with sesam: SQLite, e-mail and password,
// and one page that needs a signed-in user.
//
//	go run ./examples/basic                       # http://localhost:8080
//	curl -c jar -H 'Content-Type: application/json' \
//	  -d '{"email":"anna@example.com","password":"a long password"}' \
//	  localhost:8080/api/auth/sign-up
//	curl -b jar localhost:8080/me
package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"net/http"
	"time"

	_ "modernc.org/sqlite"

	"github.com/danielgtmn/sesam"
	"github.com/danielgtmn/sesam/adapters/sqladapter"
	"github.com/danielgtmn/sesam/plugins/password"
)

// logMailer prints reset links instead of sending them.
type logMailer struct{}

func (logMailer) SendPasswordReset(_ context.Context, u sesam.User, link string) error {
	log.Printf("password reset for %s: %s", u.Email, link)
	return nil
}

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	file := flag.String("db", "basic.db", "SQLite file")
	flag.Parse()

	db, err := sql.Open("sqlite", *file+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		log.Fatal(err)
	}
	store, err := sqladapter.New(db, sqladapter.SQLite)
	if err != nil {
		log.Fatal(err)
	}
	base := "http://" + *addr
	pw := password.New(password.Options{
		Mailer:   logMailer{},
		ResetURL: func(token string) string { return base + "/reset?token=" + token },
	})
	auth, err := sesam.New(sesam.Config{Adapter: store, BaseURL: base, Plugins: []sesam.Plugin{pw}})
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	if err := auth.Migrate(ctx); err != nil {
		log.Fatal(err)
	}
	go func() {
		for range time.Tick(time.Hour) {
			if err := auth.DeleteExpired(ctx); err != nil {
				log.Print(err)
			}
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/api/auth/", auth.Handler())
	mux.Handle("GET /me", auth.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur, _ := sesam.FromContext(r.Context())
		sesam.WriteJSON(w, http.StatusOK, cur.User)
	})))
	log.Printf("listening on %s", base)
	log.Fatal(http.ListenAndServe(*addr, auth.Middleware(mux)))
}

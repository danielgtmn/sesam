package schema

import "testing"

func TestMergeJoinsTables(t *testing.T) {
	got, err := Merge(
		Table{Name: "users", Fields: []Field{{Name: "email", Type: String, Unique: true}}},
		Table{Name: "sessions", Fields: []Field{{Name: "user_id", Type: String, Required: true, Ref: &Reference{Table: "users"}}}},
		Table{Name: "users", Fields: []Field{
			{Name: "email", Type: String, Unique: true}, // identical: fine
			{Name: "phone", Type: String},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "users" || got[1].Name != "sessions" {
		t.Fatalf("tables = %+v", got)
	}
	if cols := got[0].Columns(); len(cols) != 3 || cols[0] != "id" || cols[2] != "phone" {
		t.Fatalf("columns = %v", cols)
	}
	deps := Dependents(got, "users")
	if len(deps) != 1 || deps[0] != (Dependent{Table: "sessions", Field: "user_id"}) {
		t.Fatalf("dependents = %v", deps)
	}
}

func TestMergeRejects(t *testing.T) {
	cases := map[string][]Table{
		"bad table name":    {{Name: "Users"}},
		"bad field name":    {{Name: "users", Fields: []Field{{Name: "e-mail", Type: String}}}},
		"explicit id":       {{Name: "users", Fields: []Field{{Name: "id", Type: String}}}},
		"no type":           {{Name: "users", Fields: []Field{{Name: "email"}}}},
		"conflicting field": {{Name: "users", Fields: []Field{{Name: "email", Type: String}}}, {Name: "users", Fields: []Field{{Name: "email", Type: Int}}}},
		"unknown ref":       {{Name: "sessions", Fields: []Field{{Name: "user_id", Type: String, Ref: &Reference{Table: "users"}}}}},
		"int ref":           {{Name: "users"}, {Name: "sessions", Fields: []Field{{Name: "user_id", Type: Int, Ref: &Reference{Table: "users"}}}}},
		"index unknown":     {{Name: "users", Indexes: []Index{{Fields: []string{"nope"}}}}},
		"empty index":       {{Name: "users", Indexes: []Index{{}}}},
	}
	for name, tables := range cases {
		if _, err := Merge(tables...); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

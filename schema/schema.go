// Package schema describes the tables sesam and its plugins store data in.
// Plugins never write SQL: they declare tables and fields here, and an
// adapter turns the declaration into whatever its database needs.
//
// Every table has a primary key "id" of type String; it is implicit and
// must not be declared. Names are logical ("users"); an adapter may add a
// prefix ("auth_users").
package schema

import (
	"fmt"
	"regexp"
	"slices"
)

// Type is the type of a field.
type Type int

// Field types. Values passed to and returned by adapters use these Go types:
// String string, Int int64, Bool bool, Time time.Time (UTC, microsecond
// precision). A field that is not Required may also be nil.
const (
	String Type = iota + 1
	Int
	Bool
	Time
)

func (t Type) String() string {
	switch t {
	case String:
		return "string"
	case Int:
		return "int"
	case Bool:
		return "bool"
	case Time:
		return "time"
	}
	return fmt.Sprintf("Type(%d)", int(t))
}

// Field is a column of a table.
type Field struct {
	Name     string
	Type     Type
	Required bool       // NOT NULL
	Unique   bool       // unique among non-null values
	Index    bool       // non-unique index
	Ref      *Reference // the field holds the id of a row in another table
}

// Reference points a field at the primary key of another table. Adapters may
// create a foreign key for it. sesam deletes referencing rows itself (see
// Dependents), so correctness never depends on the database enforcing it.
type Reference struct {
	Table string
}

// Index is an index over several fields.
type Index struct {
	Fields []string
	Unique bool
}

// Table is a table with its fields. Id is implicit.
type Table struct {
	Name    string
	Fields  []Field
	Indexes []Index
}

// Field returns the field with the given name; "id" is a required String.
func (t Table) Field(name string) (Field, bool) {
	if name == "id" {
		return Field{Name: "id", Type: String, Required: true}, true
	}
	for _, f := range t.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Columns returns "id" followed by the declared field names.
func (t Table) Columns() []string {
	cols := make([]string, 0, len(t.Fields)+1)
	cols = append(cols, "id")
	for _, f := range t.Fields {
		cols = append(cols, f.Name)
	}
	return cols
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)

// ValidName tells whether s may be used as a table or field name.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// Merge combines declarations: tables with the same name are joined, so a
// plugin can add fields to a table another part declared. A field declared
// twice must be identical. Every reference must point at a declared table.
// The result keeps the order in which tables were first declared.
func Merge(tables ...Table) ([]Table, error) {
	var out []Table
	index := map[string]int{}
	for _, t := range tables {
		if !ValidName(t.Name) {
			return nil, fmt.Errorf("schema: invalid table name %q", t.Name)
		}
		i, ok := index[t.Name]
		if !ok {
			index[t.Name] = len(out)
			out = append(out, Table{Name: t.Name})
			i = len(out) - 1
		}
		m := &out[i]
		for _, f := range t.Fields {
			if !ValidName(f.Name) || f.Name == "id" {
				return nil, fmt.Errorf("schema: invalid field name %q in table %q", f.Name, t.Name)
			}
			if f.Type < String || f.Type > Time {
				return nil, fmt.Errorf("schema: field %s.%s has no valid type", t.Name, f.Name)
			}
			if f.Ref != nil && f.Type != String {
				return nil, fmt.Errorf("schema: reference %s.%s must be a string", t.Name, f.Name)
			}
			if old, ok := m.Field(f.Name); ok {
				if !sameField(old, f) {
					return nil, fmt.Errorf("schema: field %s.%s declared twice with different definitions", t.Name, f.Name)
				}
				continue
			}
			m.Fields = append(m.Fields, f)
		}
		for _, ix := range t.Indexes {
			if len(ix.Fields) == 0 {
				return nil, fmt.Errorf("schema: empty index in table %q", t.Name)
			}
			if !slices.ContainsFunc(m.Indexes, func(o Index) bool { return o.Unique == ix.Unique && slices.Equal(o.Fields, ix.Fields) }) {
				m.Indexes = append(m.Indexes, ix)
			}
		}
	}
	for _, t := range out {
		for _, ix := range t.Indexes {
			for _, name := range ix.Fields {
				if _, ok := t.Field(name); !ok {
					return nil, fmt.Errorf("schema: index on unknown field %s.%s", t.Name, name)
				}
			}
		}
		for _, f := range t.Fields {
			if f.Ref != nil {
				if _, ok := index[f.Ref.Table]; !ok {
					return nil, fmt.Errorf("schema: %s.%s references unknown table %q", t.Name, f.Name, f.Ref.Table)
				}
			}
		}
	}
	return out, nil
}

func sameField(a, b Field) bool {
	if a.Name != b.Name || a.Type != b.Type || a.Required != b.Required || a.Unique != b.Unique || a.Index != b.Index {
		return false
	}
	if (a.Ref == nil) != (b.Ref == nil) {
		return false
	}
	return a.Ref == nil || *a.Ref == *b.Ref
}

// Dependent is a field in another table that references a table.
type Dependent struct {
	Table string
	Field string
}

// Dependents returns the fields in tables that reference the table name.
func Dependents(tables []Table, name string) []Dependent {
	var out []Dependent
	for _, t := range tables {
		for _, f := range t.Fields {
			if f.Ref != nil && f.Ref.Table == name {
				out = append(out, Dependent{Table: t.Name, Field: f.Name})
			}
		}
	}
	return out
}

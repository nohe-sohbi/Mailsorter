package mirror

import (
	"reflect"
	"testing"
)

func TestStaleUIDs(t *testing.T) {
	cases := []struct {
		name     string
		stored   []string
		listed   []string
		complete bool
		want     []string
	}{
		{"nothing left the inbox", []string{"1", "2", "3"}, []string{"1", "2", "3"}, true, nil},
		{"two left, whole inbox listed", []string{"1", "2", "3", "4"}, []string{"1", "4"}, true, []string{"2", "3"}},
		{"inbox emptied, whole inbox listed", []string{"5", "6"}, nil, true, []string{"5", "6"}},
		{"partial listing judges only its own range", []string{"10", "50", "120", "130"}, []string{"100", "130"}, false, []string{"120"}},
		{"partial listing with nothing listed judges nothing", []string{"1", "2"}, nil, false, nil},
		{"a non-numeric id is never stale", []string{"abc", "2"}, []string{"1"}, true, []string{"2"}},
		{"new mail listed but not yet stored", []string{"1"}, []string{"1", "2"}, true, nil},
	}
	for _, c := range cases {
		if got := StaleUIDs(c.stored, c.listed, c.complete); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: StaleUIDs = %v, want %v", c.name, got, c.want)
		}
	}
}

package vtimezone

import (
	"bytes"
	"github.com/emersion/go-ical"
	"github.com/martin-sucha/timezones"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestToLocationTemplate(t *testing.T) {
	tests := []struct {
		filename string
		expected timezones.LocationTemplate
	}{
		{
			filename: "go49951.ical",
			expected: timezones.LocationTemplate{
				Name:    "go49951.ical",
				Zones:   nil,
				Changes: nil,
				Extend:  "<CET>-01:00:00<CEST>-02:00:00M3.5.0/02:00:00M10.5.0/03:00:00",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.filename, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", test.filename))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			dec := ical.NewDecoder(f)
			for {
				cal, err := dec.Decode()
				if err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}

				lt, err := ToLocationTemplate(test.filename, cal.Component)
				if err != nil {
					t.Fatal(err)
				}
				if lt.Name != test.expected.Name {
					t.Fatalf("name does not match, got %q, expected %q", lt.Name, test.expected.Name)
				}
				if lt.Extend != test.expected.Extend {
					t.Fatalf("extend does not match, got %q, expected %q", lt.Extend, test.expected.Extend)
				}
				if !reflect.DeepEqual(lt.Zones, test.expected.Zones) {
					t.Fatalf("zones do not match, got %+v, expected %+v", lt.Zones, test.expected.Zones)
				}
				if !reflect.DeepEqual(lt.Changes, test.expected.Changes) {
					t.Fatalf("changes do not match, got %+v, expected %+v", lt.Changes, test.expected.Changes)
				}
			}
		})
	}
}

var benchLoc *timezones.LocationTemplate

func BenchmarkToLocationTemplate(b *testing.B) {
	data, err := os.ReadFile(filepath.Join("testdata", "go49951.ical"))
	if err != nil {
		b.Fatal(err)
	}

	for i := 0; i < b.N; i++ {
		dec := ical.NewDecoder(bytes.NewReader(data))
		for {
			cal, err := dec.Decode()
			if err == io.EOF {
				break
			} else if err != nil {
				b.Fatal(err)
			}

			for _, child := range cal.Children {
				if child.Name == ical.CompTimezone {
					lt, err := ToLocationTemplate("", child)
					if err != nil {
						b.Fatal(err)
					}
					benchLoc = lt
				}
			}
		}
	}
}

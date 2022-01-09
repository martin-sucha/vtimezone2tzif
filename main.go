package main

import (
	"fmt"
	"github.com/emersion/go-ical"
	"github.com/martin-sucha/timezones"
	"github.com/martin-sucha/vtimezone2tzif/vtimezone"
	"io"
	"os"
)

func main() {
	err := mainErr()
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
}

func mainErr() error {
	dec := ical.NewDecoder(os.Stdin)
	processed := false
	for {
		cal, err := dec.Decode()
		if err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}

		if processed {
			return fmt.Errorf("only one VTIMEZONE can be present")
		}

		lt, err := vtimezone.ToLocationTemplate("", cal.Component)
		if err != nil {
			return err
		}
		data, err := timezones.TZData(*lt)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(data)
		if err != nil {
			return err
		}
	}
}

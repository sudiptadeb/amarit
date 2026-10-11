package main

import (
	"strings"
	"testing"

	"github.com/sudiptadeb/amarit"
)

func TestMatchUnits(t *testing.T) {
	units := []amarit.Unit{
		{Name: "model-server", Binary: "/x"},
		{Name: "web", Binary: "/x"},
		{Name: "worker", Binary: "/x", Instances: 3},
	}
	got, err := matchUnits([]string{"worker@2", "w*"}, units)
	if err != nil {
		t.Fatal(err)
	}
	if want := "worker@2 web worker@1 worker@3"; strings.Join(got, " ") != want {
		t.Fatalf("matched %q, want %q", strings.Join(got, " "), want)
	}
	if _, err := matchUnits([]string{"nothing-*"}, units); err == nil {
		t.Fatal("a pattern that matches nothing must be an error")
	}
	args, rolling := takeFlag([]string{"web", "-rolling"}, "rolling")
	if !rolling || len(args) != 1 {
		t.Fatalf("takeFlag left %v, rolling=%v", args, rolling)
	}
}

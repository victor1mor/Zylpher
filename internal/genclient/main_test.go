package main

import (
	"bytes"
	"os"
	"testing"
)

func TestGeneratedClientIsCurrent(t *testing.T) {
	spec, err := os.ReadFile("../../openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := generate(spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../client/client.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
		t.Fatal("client differs from openapi.json; run go generate ./client")
	}
}

package core

import "testing"

func TestClientFlags(t *testing.T) {
	if got := clientFlags("bN"); got != "blocked" {
		t.Error(got)
	}
	if got := clientFlags("xMx"); got != "in MULTI, primary" {
		t.Error(got)
	}
}
